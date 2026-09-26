package server

import (
	"context"
	"net/http"
	"time"

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
}

// ticketViews attaches each ticket's live SLA status via one batch call
// (sla.Service.StatusesFor), so the cost is two extra queries per response
// regardless of how many tickets are on the page. Callers pass the page
// after it has already been paged/sliced/merged — never before, which would
// compute SLA for rows the response throws away (see the staff "mine" scope
// merge in handleListTickets and CLAUDE.md's "Ticket list paging" note).
//
// SLA visibility is staff/admin only: a reporting user must not see the
// indicator, the policy name, the targets, or the breach/late status on their
// own ticket, so actor == RoleUser gets "sla": null unconditionally — the same
// null a client already treats as "no indicator" when there is no matching
// policy, per #183. actor is nil only defensively (every route that reaches
// here requires an authenticated actor); treated the same as RoleUser so a
// missed check fails closed rather than leaking the field.
//
// When SLA tracking is off, or the actor cannot see it at all, every SLA
// field is nil and the store is never called: an instance with the feature
// disabled, or a request from a reporting user, pays nothing for it.
func (s *Server) ticketViews(ctx context.Context, ts []ticket.Ticket, actor *authmw.Actor) ([]ticketView, error) {
	views := make([]ticketView, len(ts))
	for i, t := range ts {
		views[i] = ticketView{Ticket: t}
	}
	if actor == nil || actor.Role == user.RoleUser {
		return views, nil
	}
	if !s.adminSvc.SLAEnabled(ctx) {
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
// page it was given and write the result as JSON. Every list branch in that
// handler goes through here, so the wrapper runs exactly once per request.
func (s *Server) writeTickets(w http.ResponseWriter, r *http.Request, tickets []ticket.Ticket) {
	views, err := s.ticketViews(r.Context(), tickets, authmw.GetActor(r))
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, views)
}
