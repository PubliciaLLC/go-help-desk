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
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
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
	// Names resolves the ids in Before/After to what they name now (#422),
	// keyed by the id as it appears there. An id with no entry no longer
	// resolves; show the id. Before/After keep the ids exactly as stored.
	Names map[string]auditName `json:"names,omitempty"`
}

type adminAuditListResponse struct {
	Entries []adminAuditEntryView `json:"entries"`
	// Total is the number of entries matching the filter, up to audit.TotalCap
	// (see TotalCapped).
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
// ticket. Administrators only (Erik, 2026-11-03). Staff have the history of a
// ticket they can see (handleListTicketAudit) and nothing wider: this view
// used to be open to them, narrowed to ticket entries in their scope (#331),
// and that narrowing went when the access did. The route sits under
// adminRouter, so staff get 403 whatever credential they come with.
//
// A signed-in session needs no scope; a machine credential needs audit:read
// (#362). tickets:read does not reach it, because for an admin's key it
// covers every entity.
//
// Entries are shown with their diff, run through audit.Redact: the allow-list
// of what the writers record applies to an administrator too.
func (s *Server) handleListAdminAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	f, limit, offset, err := parseAdminAuditQuery(r)
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	page, err := s.auditStore.Search(ctx, f, limit, offset)
	if err != nil {
		handleError(w, err)
		return
	}
	entries := page.Entries

	maskRequesters := s.masksRequestersIn(ctx, admin.MaskRequesterNamesAdminLog)
	views := make([]adminAuditEntryView, len(entries))
	names := make(map[uuid.UUID]auditActor)
	diffs := make([]map[string]any, 0, 2*len(entries))
	for i, e := range entries {
		v := adminAuditEntryView{
			ID: e.ID, EntityType: e.EntityType, EntityID: e.EntityID,
			Action: e.Action, ActorID: e.ActorID, CreatedAt: e.CreatedAt,
		}
		v.Before, v.After = audit.Redact(e.Before, e.After)
		diffs = append(diffs, v.Before, v.After)
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

	// Names come from the diffs as sent, so what the diff gate and audit.Redact
	// withheld is never resolved. One batch for the page.
	if len(diffs) > 0 {
		resolved := s.resolveAuditNames(ctx, maskRequesters, diffs...)
		for i := range views {
			views[i].Names = resolved.forEntry(views[i].Before, views[i].After)
		}
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

	// exclude_action (repeatable) leaves those actions out, in the query, so
	// the page, the offset and the total describe one sequence. It only ever
	// narrows what the caller may see, so it needs no gate of its own.
	for _, v := range q["exclude_action"] {
		v = strings.TrimSpace(v)
		if strings.ContainsRune(v, 0) || !utf8.ValidString(v) {
			return audit.Filter{}, 0, 0, errBadText
		}
		if v != "" {
			f.ExcludeActions = append(f.ExcludeActions, v)
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
