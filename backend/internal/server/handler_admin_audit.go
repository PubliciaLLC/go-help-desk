package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// adminAuditEntryView is one row of the admin-wide audit view. Shaped like
// ticketAuditEntryView plus the entity the per-ticket feed doesn't need to
// say (it's always the one ticket in the URL); the diff and redaction rules
// are identical — see handleListAdminAudit's own comment.
type adminAuditEntryView struct {
	ID         uuid.UUID      `json:"id"`
	EntityType string         `json:"entity_type"`
	EntityID   uuid.UUID      `json:"entity_id"`
	Action     string         `json:"action"`
	ActorID    *uuid.UUID     `json:"actor_id"`
	ActorName  string         `json:"actor_name,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	Before     map[string]any `json:"before,omitempty"`
	After      map[string]any `json:"after,omitempty"`
}

type adminAuditListResponse struct {
	Entries []adminAuditEntryView `json:"entries"`
	Total   int                   `json:"total"`
}

// adminAuditFetchMultiplier over-fetches when a staff viewer's ticket-scope
// filter will remove some rows after the query runs, so a page is less
// likely to come back shorter than asked for. Not a guarantee — see
// handleListAdminAudit's own comment on why an exact scoped page is future
// work, not this version's job.
const adminAuditFetchMultiplier = 4

// adminAuditFetchCap bounds the over-fetch above so a narrow filter on a
// large table cannot be turned into an unbounded query by asking for a huge
// limit.
const adminAuditFetchCap = 1000

// GET /api/v1/admin/audit
//
// #129's admin-wide half: searchable across every entity, not just one
// ticket. Staff and admin only — a reporting user has no route here at all.
// Gated by RequireResource(auth.ResourceTickets), the same as the per-ticket
// feed's own route: an API key at staff or admin level reaches this exactly
// as it reaches every other ticket-adjacent read.
//
// Staff are narrowed twice, independently:
//  1. entity_type is forced to "ticket" regardless of what the query string
//     asks for — every other entity type (user, SLA policy, category, ...)
//     is admin-only, full stop, unrelated to the staff setting below.
//  2. Within those ticket entries, only tickets in the caller's own scope
//     (the same CanViewTicket rule as everywhere else) survive — applied
//     after Search runs, in Go, per audit.Store.Search's own documented
//     limit: it does not know about ticket scope, so a caller that must not
//     see every entity filters the result itself. That means Total can
//     overcount what a scoped staff viewer actually sees (it is Search's
//     own count, before scope narrowing) and a page can come back shorter
//     than limit asked for even with the over-fetch cushion above. Good
//     enough for a first version — see docs/DESIGN.md's audit section for
//     why an exact scope-aware count is future work, not this one's.
//
// The field-level diff is the same two gates as the per-ticket feed
// (handleListTicketAudit, which this deliberately mirrors): admin always
// sees it; staff only with admin.KeyStaffCanViewTicketChangeHistory on;
// redacted either way — see audit.Redact.
func (s *Server) handleListAdminAudit(w http.ResponseWriter, r *http.Request) {
	role := authmw.GetActor(r).Role
	ctx := r.Context()

	f, limit, offset, err := parseAdminAuditQuery(r)
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	if role == user.RoleStaff {
		f.EntityType = "ticket"
	}

	fetchLimit := limit
	if role == user.RoleStaff {
		fetchLimit = min(limit*adminAuditFetchMultiplier, adminAuditFetchCap)
	}

	entries, total, err := s.auditStore.Search(ctx, f, fetchLimit, offset)
	if err != nil {
		handleError(w, err)
		return
	}

	if role == user.RoleStaff {
		entries, err = s.filterToVisibleTickets(r, entries)
		if err != nil {
			handleError(w, err)
			return
		}
		if len(entries) > limit {
			entries = entries[:limit]
		}
	}

	showDiff := role == user.RoleAdmin ||
		(role == user.RoleStaff && s.adminSvc.StaffCanViewTicketChangeHistory(ctx))

	views := make([]adminAuditEntryView, len(entries))
	names := make(map[uuid.UUID]string)
	for i, e := range entries {
		v := adminAuditEntryView{
			ID: e.ID, EntityType: e.EntityType, EntityID: e.EntityID,
			Action: e.Action, ActorID: e.ActorID, CreatedAt: e.CreatedAt,
		}
		if showDiff {
			v.Before, v.After = audit.Redact(e.Before, e.After)
		}
		if e.ActorID != nil {
			name, cached := names[*e.ActorID]
			if !cached {
				u, err := s.users.GetByID(ctx, *e.ActorID)
				switch {
				case err == nil:
					name = u.DisplayName
				case errors.Is(err, user.ErrNotFound):
					// Account since deleted; actor_id stays, only the name is left blank.
				default:
					slog.ErrorContext(ctx, "resolving admin audit actor name failed", "actor_id", *e.ActorID, "error", err)
				}
				names[*e.ActorID] = name
			}
			v.ActorName = name
		}
		views[i] = v
	}

	JSON(w, http.StatusOK, adminAuditListResponse{Entries: views, Total: total})
}

// filterToVisibleTickets keeps only the entries whose ticket the request's
// actor may currently see, per CanViewTicket — the same rule that gates
// GET /tickets/{id}. Every entry passed in must have EntityType == "ticket";
// callers other than handleListAdminAudit that don't hold that invariant
// would need to check it themselves first.
//
// A lookup failure (the referenced ticket cannot be read) drops the entry
// rather than failing the whole request — the caller cannot tell "does not
// exist" from "database hiccup" from here, and either way this fails
// closed: an entry this cannot verify as visible is not shown, not shown
// anyway.
func (s *Server) filterToVisibleTickets(r *http.Request, entries []audit.Entry) ([]audit.Entry, error) {
	actor := authmw.GetActor(r)
	ctx := r.Context()
	// Cached per request: several entries commonly name the same ticket
	// (created, then resolved, then closed), and a scope check is two more
	// lookups (groups, scope rules) behind CanViewTicket — one GetByID plus
	// one scope check per DISTINCT ticket, not per entry.
	visible := make(map[uuid.UUID]bool)

	kept := make([]audit.Entry, 0, len(entries))
	for _, e := range entries {
		ok, cached := visible[e.EntityID]
		if !cached {
			t, err := s.tickets.GetByID(ctx, e.EntityID)
			if err != nil {
				slog.WarnContext(ctx, "admin audit: could not load a referenced ticket, dropping its entry",
					"ticket_id", e.EntityID, "error", err)
				visible[e.EntityID] = false
				continue
			}
			ok, err = s.CanViewTicket(ctx, actor, t)
			if err != nil {
				return nil, err
			}
			visible[e.EntityID] = ok
		}
		if ok {
			kept = append(kept, e)
		}
	}
	return kept, nil
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
)
