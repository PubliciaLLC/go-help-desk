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
	// DetailsSearched is true when q was also looked for in the change details,
	// which happens only inside a date range of at most audit.DetailsSearchMaxDays.
	// False without q, and false when q was matched on the entity type, the action
	// and the actor alone; DetailsSkippedReason then says why. Both always sent,
	// for the reason TotalCapped is.
	DetailsSearched bool `json:"details_searched"`
	// DetailsSkippedReason is audit.DetailsSkippedNoRange or
	// audit.DetailsSkippedRangeTooLong when q was sent and the details were not
	// searched, and absent otherwise.
	DetailsSkippedReason string `json:"details_skipped_reason,omitempty"`
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

	// The same decision masks the names in the page and keeps q from finding a
	// requester by name: a search that answered "yes" for a requester's name
	// would identify them as well as showing it would.
	maskRequesters := s.masksRequestersIn(ctx, admin.MaskRequesterNamesAdminLog)
	f.MaskRequesterActors = maskRequesters

	page, err := s.auditStore.Search(ctx, f, limit, offset)
	if err != nil {
		handleError(w, err)
		return
	}
	entries := page.Entries

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
					// Account since deleted; actor_id stays. Its name is not sent, and
					// the view says so, of any role (owner, 2026-11-11).
					a = auditActor{name: deletedAccountName}
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
		Entries: views, Total: page.Total, TotalCapped: page.TotalCapped,
		DetailsSearched: f.SearchesDetails(), DetailsSkippedReason: f.DetailsSkippedReason(), HasMore: page.HasMore,
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

	// q is sent to the database once for every searchable key, so its length is
	// bounded. Nobody types a longer search; a longer one is a mistake or a probe.
	if utf8.RuneCountInString(f.Q) > maxAuditQueryRunes {
		return audit.Filter{}, 0, 0, errQTooLong
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

// maxAuditQueryRunes bounds q (#629).
const maxAuditQueryRunes = 200

var (
	errQTooLong   = errors.New("q must be at most 200 characters")
	errBadActorID = errors.New("actor_id must be a UUID")
	errBadFrom    = errors.New("from must be an RFC3339 timestamp")
	errBadTo      = errors.New("to must be an RFC3339 timestamp")
	errBadText    = errors.New("filters must be valid UTF-8 without NUL bytes")
)

// deletedAccountName is what every place that names a person in audit output
// shows for one it cannot resolve (owner, 2026-11-11): accurate, the same for
// every role, and never the name. Where the mask is on, it is also what stands
// in for a requester's name once the account is gone, so it tells nobody
// which role the account had.
const deletedAccountName = "Deleted account"

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

// auditMaskChange records a change to the requester-name mask (D35), when the
// value in force is no longer the one it was before the save. Loosening the
// mask is the one settings change that shows requester names, so it must leave
// a trace of who did it and when; a save that leaves the value as it was
// records nothing.
//
// Best effort and detached from the request, like the API key entries
// (writeAudit): a mask that read back as a failure would block the change it
// describes, and an administrator who hangs up right after saving must not be
// able to suppress the entry. Like those entries it is not transactional with
// the write, so a crash between the two loses the entry; the setting is
// admin-only, so the person who could change it unrecorded already holds the
// capability, as for a factor reset.
func (s *Server) auditMaskChange(ctx context.Context, actor uuid.UUID, before string) {
	after := s.adminSvc.AuditMaskRequesterNames(ctx)
	if after == before || s.auditStore == nil {
		return
	}
	s.writeAudit(ctx, audit.Entry{
		ID: uuid.New(), ActorID: &actor, EntityType: auditEntitySetting,
		EntityID: settingEntityID(admin.KeyAuditMaskRequesterNames), Action: auditActionMaskChanged,
		Before: map[string]any{"requester_name_mask": before}, After: map[string]any{"requester_name_mask": after},
		CreatedAt: time.Now(),
	})
}

// saveAuditMask stores the mask setting and records the change, both detached
// from the request. The read-back of the new value in auditMaskChange is a
// database call too: on a cancelled request context it fails, reads as the
// default, and an entry for a real change is skipped. The save is detached for
// the reason the audit write is: once it commits, a client that hangs up must
// not get a false 500 for a change that landed (pgx reports `context canceled`
// after an autocommit write completes), nor suppress the entry.
func (s *Server) saveAuditMask(ctx context.Context, actor uuid.UUID, raw []byte, before string) error {
	ctx = context.WithoutCancel(ctx)
	if err := s.adminSvc.SetRaw(ctx, admin.KeyAuditMaskRequesterNames, raw); err != nil {
		return err
	}
	s.auditMaskChange(ctx, actor, before)
	return nil
}

const (
	auditEntitySetting     = "setting"
	auditActionMaskChanged = "audit_mask_changed"
)

// settingEntityID is the audit entity id for a setting, which has a name and no
// row id; the audit log keys on a UUID. Derived the way emailTemplateEntityID is.
func settingEntityID(key string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("setting:"+key))
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
