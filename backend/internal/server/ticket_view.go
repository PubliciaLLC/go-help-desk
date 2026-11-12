package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// ticketView is a ticket as the API sends it: the domain ticket plus its
// computed SLA status. ticket.Ticket cannot carry sla.Status itself — sla
// imports ticket for Priority, so the reverse import would be a cycle — and
// the status is derived, not stored, so it belongs on the wire type, not the
// domain type.
type ticketView struct {
	ticket.Ticket
	SLA *sla.Status `json:"sla"` // nil: no matching policy, SLA tracking is off, or the viewer is a reporting user

	// CanReopen is whether THIS viewer may force-reopen THIS ticket: it is
	// Closed and closed_reopen_policy lets the viewer's role (#349). It is how
	// the ticket page knows whether to show the Reopen button, and a courtesy
	// only — the service decides again, on the locked row, when it is used.
	// False for a reporting user whatever the setting, and false for any
	// ticket that is not Closed.
	CanReopen bool `json:"can_reopen"`

	// Requester is who the ticket is for: the reporting account's name and
	// address, or the guest's (#444). Sent on the single-ticket read to staff
	// and administrators only, on every ticket. Never to a requester, who is on
	// their own ticket and whom this would tell nothing: it is not on the
	// requester allowed list, so FilterForViewer drops it. Absent when the
	// account cannot be read.
	Requester *ticketRequester `json:"requester,omitempty"`

	// FollowUp is the follow-up already opened from this ticket, when it is
	// Closed and has one (#625): the newest ticket the follow-up action made
	// from it. It is how the ticket page stops offering a follow-up that would
	// be refused, and links to the one that exists. Set on the single-ticket
	// read only, so a list pays no lookup; sent to every role, since it is a
	// requester's own new ticket. Absent otherwise.
	FollowUp *ticket.FollowUpRef `json:"follow_up,omitempty"`

	// ReplyMatch is the reply a reply search found this ticket through: an
	// excerpt, never an author. Present only on a list that searched replies and
	// only for a ticket that matched through one. It is on the requester allowed
	// list, so a requester gets it too: the store reads it with the same
	// IncludeInternal that chose the page, which is false for them, so it is never
	// an internal note.
	ReplyMatch *ticket.ReplyMatch `json:"reply_match,omitempty"`

	// HasQuarantinedAttachment is whether any file on this ticket was held by
	// the scanner (attachments.virus_name IS NOT NULL), so staff scanning the
	// queue can tell without opening each ticket (#628). Staff and
	// administrators only: it is not on the requester allowed list, and
	// ticketViews does not look it up for anyone else. omitempty, so a ticket
	// without one carries no key at all.
	HasQuarantinedAttachment bool `json:"has_quarantined_attachment,omitempty"`
}

// ticketRequester is the Requester line of the staff ticket page.
type ticketRequester struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// requesterFor is the Requester line for this viewer: requesterOf for staff and
// administrators, nil for anyone else (a reporting user, an unknown role, no
// actor). It is the handler's layer of keeping the line from requesters, which
// also means no lookup is made for them; the other layer is the field filter
// (the key is not on the requester allowed list). Each is tested on its own.
func (s *Server) requesterFor(ctx context.Context, a *authmw.Actor, t ticket.Ticket) *ticketRequester {
	if a == nil || (a.Role != user.RoleStaff && a.Role != user.RoleAdmin) {
		return nil
	}
	return s.requesterOf(ctx, t)
}

// requesterOf names who a ticket is for. A deleted account is still named: the
// ticket remains, and staff reading it need to know who it was about. nil when
// there is nobody to name or the account cannot be read, which is logged: the
// ticket is still sent, without its Requester line.
func (s *Server) requesterOf(ctx context.Context, t ticket.Ticket) *ticketRequester {
	if t.ReporterUserID != nil {
		u, err := s.users.GetByIDAdmin(ctx, *t.ReporterUserID)
		if err != nil {
			slog.WarnContext(ctx, "reading a ticket's reporting account failed; omitting its requester line",
				"ticket_id", t.ID, "error", err)
			return nil
		}
		return &ticketRequester{Name: u.DisplayName, Email: u.Email}
	}
	if t.GuestEmail != nil {
		return &ticketRequester{Name: t.GuestName, Email: *t.GuestEmail}
	}
	return nil
}

// viewerJSON is the exit point for any response that carries tickets: staff and
// administrators get it as built, everyone else gets only what the requester
// allowed list permits (ticket.FilterForViewer). A nil actor is treated as a
// requester, so a missed check fails closed.
func viewerJSON(actor *authmw.Actor, v any) any {
	role := user.Role("")
	if actor != nil {
		role = actor.Role
	}
	out, err := ticket.FilterForViewer(role, v)
	if err != nil {
		// Encoding a value this package just built does not fail; if it ever does,
		// send nothing rather than the unfiltered ticket.
		return map[string]string{}
	}
	return out
}

// ticketViews attaches each ticket's live SLA status via one batch call
// (sla.Service.StatusesFor), so the cost is two extra queries per response
// regardless of how many tickets are on the page. Callers pass the page
// after it has already been paged — never before, which would compute SLA for
// rows the response throws away (a list's page is cut in SQL now; it used to be
// cut in Go after a merge, see DESIGN.md's "Ticket list paging" note).
//
// SLA visibility is staff/admin only: a reporting user must not see the
// indicator, the policy name, the targets, or the breach/late status on their
// own ticket, so any actor that is not staff or an administrator gets
// "sla": null unconditionally — the same null a client already treats as "no
// indicator" when there is no matching policy, per #183. actor is nil only
// defensively (every route that reaches here requires an authenticated actor);
// treated the same so a missed check fails closed rather than leaking the field.
//
// When SLA tracking is off, or the actor cannot see it at all, every SLA
// field is nil and the store is never called: an instance with the feature
// disabled, or a request from a reporting user, pays nothing for it.
func (s *Server) ticketViews(ctx context.Context, ts []ticket.Ticket, actor *authmw.Actor) ([]ticketView, error) {
	views := make([]ticketView, len(ts))
	for i, t := range ts {
		views[i] = ticketView{Ticket: t}
	}
	// Only staff and administrators get the SLA status, can_reopen and the
	// quarantine marker. Any other role, one this code does not recognise
	// included, gets none of them.
	if actor == nil || (actor.Role != user.RoleStaff && actor.Role != user.RoleAdmin) {
		return views, nil
	}
	// Read once per response, and only when a closed ticket is in it.
	policy := ""
	for i, t := range ts {
		if t.ClosedAt == nil {
			continue
		}
		if policy == "" {
			policy = s.adminSvc.ClosedReopenPolicy(ctx)
		}
		views[i].CanReopen = ticket.CanForceReopen(policy, actor.Role)
	}
	// One query for the page. A failure leaves the marker off rather than
	// failing the list: it is an aid to scanning the queue, and the ticket page
	// still shows the warning per file.
	ids := make([]uuid.UUID, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	quarantined, err := s.tickets.QuarantinedTicketIDs(ctx, ids)
	if err != nil {
		slog.WarnContext(ctx, "reading which tickets hold quarantined attachments failed; omitting the marker", "error", err)
	}
	for i := range views {
		views[i].HasQuarantinedAttachment = quarantined[views[i].ID]
	}
	enabled, err := s.adminSvc.SLAEnabled(ctx)
	if err != nil {
		// Fail safe: treat a read failure as "disabled" rather than let it
		// surface as a 500 on every ticket list, but log it — see
		// admin.Service.SLAEnabled and #216.
		slog.WarnContext(ctx, "reading SLA enabled setting failed; omitting SLA status", "error", err)
		return views, nil
	}
	if !enabled {
		return views, nil
	}
	statuses, err := s.slaPolicies.StatusesFor(ctx, ts, time.Now())
	if err != nil {
		return nil, err
	}
	for i := range views {
		if st, ok := statuses[views[i].ID]; ok {
			st := st
			views[i].SLA = &st
		}
	}
	return views, nil
}

// writeTickets is handleListTickets' common exit: attach SLA status to the
// page it was given and write the result as JSON. The wrapper runs exactly once
// per request.
func (s *Server) writeTickets(w http.ResponseWriter, r *http.Request, tickets []ticket.Ticket, matches map[uuid.UUID]ticket.ReplyMatch) {
	views, err := s.ticketViews(r.Context(), tickets, authmw.GetActor(r))
	if err != nil {
		handleError(w, err)
		return
	}
	for i := range views {
		if m, ok := matches[views[i].ID]; ok {
			views[i].ReplyMatch = &m
		}
	}
	JSON(w, http.StatusOK, viewerJSON(authmw.GetActor(r), views))
}
