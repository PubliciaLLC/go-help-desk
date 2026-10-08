package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// adminAuditEntryView is one row of the admin-wide audit view. Shaped like
// ticketAuditEntryView plus the entity the per-ticket feed doesn't need to
// say (it's always the one ticket in the URL); the diff and redaction rules
// are identical — see handleListAdminAudit's own comment.
type adminAuditEntryView struct {
	ID          uuid.UUID      `json:"id"`
	EntityType  string         `json:"entity_type"`
	EntityID    uuid.UUID      `json:"entity_id"`
	Action      string         `json:"action"`
	ActorID     *uuid.UUID     `json:"actor_id"`
	ActorName   string         `json:"actor_name,omitempty"`
	ActorMasked bool           `json:"actor_masked,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Before      map[string]any `json:"before,omitempty"`
	After       map[string]any `json:"after,omitempty"`
}

type adminAuditListResponse struct {
	Entries []adminAuditEntryView `json:"entries"`
	// Total is the number of entries matching the filter that the caller may
	// see, up to audit.TotalCap (see TotalCapped): for staff under scope
	// enforcement that is the count of entries on tickets in their scope, from
	// the same predicate that picks the page.
	//
	// It used to be withheld from staff. It was Search's own count, taken
	// before scope was applied in Go, so a staff member who could see none of
	// the matching entries still learned how many there were — with actor_id,
	// action and from/to available, an oracle for dating activity on tickets
	// they have no scope over. Scope is now part of the count's own WHERE
	// clause, so there is nothing to withhold: the number says how many entries
	// the caller can page through, and nothing about the ones they cannot.
	Total int `json:"total"`
	// TotalCapped is true when more than audit.TotalCap entries match. Total is
	// then audit.TotalCap and means "at least this many": counting a table
	// nobody prunes exactly is a full scan per page view (#331), so the count
	// stops there. Always sent, false included, so a client does not have to
	// tell "not capped" from "an older server".
	TotalCapped bool `json:"total_capped"`
	// HasMore reports whether another page exists. It is read off the page
	// itself (one row past it), not derived from Total, which stops at the cap
	// while a pager keeps going. This is the client's only paging rule.
	HasMore bool `json:"has_more"`
}

// GET /api/v1/admin/audit
//
// #129's admin-wide half: searchable across every entity, not just one
// ticket. Staff and admin only — a reporting user has no route here at all.
// Gated by RequireResource(auth.ResourceAudit) (#362): a signed-in session
// needs no scope, a machine credential needs audit:read. tickets:read does
// not reach it, because for an admin's key it covers every entity.
//
// Staff are narrowed twice, independently:
//  1. entity_type is forced to "ticket" regardless of what the query string
//     asks for — every other entity type (user, SLA policy, category, ...)
//     is admin-only, full stop, unrelated to the staff setting below.
//  2. With ticket scope enforced, only tickets in the caller's own scope
//     survive. That is applied by the query itself (audit.Filter.ScopedTo),
//     with the same predicate the scoped ticket listing uses, so the page, the
//     offset and the total all describe one sequence — see the parity test in
//     handler_admin_audit_scope_test.go, which holds that predicate equal to
//     ticket.CanView. With enforcement off every ticket is visible to staff and
//     nothing is applied.
//
// The field-level diff is the same two gates as the per-ticket feed
// (handleListTicketAudit, which this deliberately mirrors): admin always
// sees it; staff only with admin.KeyStaffCanViewTicketChangeHistory on;
// redacted either way — see audit.Redact.
func (s *Server) handleListAdminAudit(w http.ResponseWriter, r *http.Request) {
	actor := authmw.GetActor(r)
	role := actor.Role
	ctx := r.Context()

	f, limit, offset, err := parseAdminAuditQuery(r)
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	if role == user.RoleStaff {
		f.EntityType = "ticket"
		vis, err := s.TicketVisibility(ctx, actor)
		if err != nil {
			handleError(w, err)
			return
		}
		// Anything short of "all" narrows, so a mode this handler does not
		// expect fails closed rather than open.
		if vis != ticket.VisibilityAll {
			f.ScopedTo = &actor.UserID
		}
	}

	page, err := s.auditStore.Search(ctx, f, limit, offset)
	if err != nil {
		handleError(w, err)
		return
	}
	entries := page.Entries

	showDiff := role == user.RoleAdmin ||
		(role == user.RoleStaff && s.adminSvc.StaffCanViewTicketChangeHistory(ctx))

	maskRequesters := s.masksRequestersIn(ctx, admin.MaskRequesterNamesAdminLog)
	views := make([]adminAuditEntryView, len(entries))
	names := make(map[uuid.UUID]auditActor)
	for i, e := range entries {
		v := adminAuditEntryView{
			ID: e.ID, EntityType: e.EntityType, EntityID: e.EntityID,
			Action: e.Action, ActorID: e.ActorID, CreatedAt: e.CreatedAt,
		}
		if showDiff {
			v.Before, v.After = audit.Redact(e.Before, e.After)
		}
		if e.ActorID != nil {
			a, cached := names[*e.ActorID]
			if !cached {
				u, err := s.users.GetByID(ctx, *e.ActorID)
				switch {
				case err == nil:
					a = auditActorName(u, maskRequesters)
				case errors.Is(err, user.ErrNotFound):
					// Account since deleted; actor_id stays, only the name is left blank.
				default:
					slog.ErrorContext(ctx, "resolving admin audit actor name failed", "actor_id", *e.ActorID, "error", err)
				}
				names[*e.ActorID] = a
			}
			v.ActorName = a.name
			v.ActorMasked = a.masked
		}
		views[i] = v
	}

	JSON(w, http.StatusOK, adminAuditListResponse{
		Entries: views, Total: page.Total, TotalCapped: page.TotalCapped, HasMore: page.HasMore,
	})
}

// parseAdminAuditQuery reads the optional filters and pagination off the
// query string. Every filter left blank/absent matches everything — see
// audit.Filter's own zero-value contract.
func parseAdminAuditQuery(r *http.Request) (f audit.Filter, limit, offset int, err error) {
	q := r.URL.Query()
	limit, offset = pageParams(r)

	f.EntityType = strings.TrimSpace(q.Get("entity_type"))
	f.Action = strings.TrimSpace(q.Get("action"))
	f.Q = strings.TrimSpace(q.Get("q"))
	// Postgres text cannot hold NUL or invalid UTF-8; letting either reach
	// the query turns a malformed request into a 500.
	for _, v := range []string{f.EntityType, f.Action, f.Q} {
		if strings.ContainsRune(v, 0) || !utf8.ValidString(v) {
			return audit.Filter{}, 0, 0, errBadText
		}
	}

	if v := strings.TrimSpace(q.Get("actor_id")); v != "" {
		id, parseErr := uuid.Parse(v)
		if parseErr != nil {
			return audit.Filter{}, 0, 0, errBadActorID
		}
		f.ActorID = &id
	}

	if v := strings.TrimSpace(q.Get("from")); v != "" {
		t, parseErr := time.Parse(time.RFC3339, v)
		if parseErr != nil {
			return audit.Filter{}, 0, 0, errBadFrom
		}
		f.From = &t
	}

	if v := strings.TrimSpace(q.Get("to")); v != "" {
		t, parseErr := time.Parse(time.RFC3339, v)
		if parseErr != nil {
			return audit.Filter{}, 0, 0, errBadTo
		}
		f.To = &t
	}

	return f, limit, offset, nil
}

var (
	errBadActorID = errors.New("actor_id must be a UUID")
	errBadFrom    = errors.New("from must be an RFC3339 timestamp")
	errBadTo      = errors.New("to must be an RFC3339 timestamp")
	errBadText    = errors.New("filters must be valid UTF-8 without NUL bytes")
)

// auditActor is what an audit view shows for one actor.
type auditActor struct {
	name   string
	masked bool
}

// auditActorName is the name an audit view shows for an actor (#362, Erik).
// Staff and administrators are named: the audit trail exists to say which
// employee did what. A requester (a student, a patient, a customer) is
// shown as "Requester" where the view masks them (masksRequestersIn), and
// masked is true so a client can tell that label from an account whose
// display name happens to be "Requester" (#366). The entry keeps its
// actor_id, so it stays attributable for an investigation.
//
// The role is the actor's role now, not when the entry was written (#366):
// a staff member later made a user is masked on their old entries, and a
// requester promoted to staff is named on theirs. Display-only: anyone
// who can see the entry can still follow actor_id or entity_id to the name.
func auditActorName(u user.User, maskRequesters bool) auditActor {
	if maskRequesters && u.Role == user.RoleUser {
		return auditActor{name: "Requester", masked: true}
	}
	return auditActor{name: u.DisplayName}
}

// masksRequestersIn reports whether the given audit view masks requester
// names under the audit_mask_requester_names setting.
func (s *Server) masksRequestersIn(ctx context.Context, view string) bool {
	switch s.adminSvc.AuditMaskRequesterNames(ctx) {
	case admin.MaskRequesterNamesEverywhere:
		return true
	case view:
		return true
	}
	return false
}
