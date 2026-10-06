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
	// Total is the number of entries matching the filter, and is null for a
	// scoped staff viewer.
	//
	// It used to be Search's own count for everybody, taken before ticket
	// scope was applied — so a staff member who could see none of the matching
	// entries still learned how many there were. With the filters this
	// endpoint accepts, that is an oracle rather than a cosmetic overcount:
	// ask for action=created&actor_id=<someone>, read the count, and narrow
	// from/to by bisection to date activity on tickets you have no scope over.
	// Entries were correctly withheld; the number was not.
	//
	// There is no scope-aware count to give instead without pushing scope
	// into the query, so staff get no count at all. HasMore is what the pager
	// actually needs.
	Total *int `json:"total"`
	// HasMore reports whether another page exists. For staff this is the only
	// honest answer, since Total is null; for admin it is redundant with
	// Total and offset, and is sent anyway so the client has one rule.
	HasMore bool `json:"has_more"`
	// Truncated says the staff walk stopped — at its row ceiling or its time
	// budget — before reaching the end of the log, so a short result means
	// "stopped looking", not "nothing left".
	// Without it the two are indistinguishable, and the one that looks like
	// an empty audit log is the wrong one to guess.
	Truncated bool `json:"truncated,omitempty"`
}

// adminAuditScanBatch is how many rows the staff path reads per round while
// walking forward to assemble a page.
//
// Staff cannot be paginated by passing offset to the query: scope is applied
// in Go afterwards, so a raw offset indexes a different sequence from the one
// the caller is reading. Doing that produced pages that overlapped — with six
// in-scope and six out-of-scope tickets interleaved and limit=2, consecutive
// pages came back [A B] [B C] [C D] [D E], every page repeating the last
// entry of the one before it.
//
// So the staff path walks from the beginning and counts VISIBLE entries,
// which is the sequence the caller actually sees.
const adminAuditScanBatch = 200

// adminAuditScanCap and adminAuditScanBudget bound that walk, by rows and by
// wall-clock time. Scanning from the beginning is O(offset) by construction,
// so without a ceiling a deep page on a large table is a table scan.
//
// The time budget is the one that matters. Round 2 of #328's review raised the
// row cap to 50,000 on the strength of "5,000 rows in 53ms" — a figure taken
// with few distinct tickets, so a cache absorbed the repeats. With distinct
// out-of-scope tickets the real cost was ~0.6ms per entry, and a 50,000-row
// walk measured 30.8s: past the server's own 30s WriteTimeout, so the client
// got a dropped connection while the handler and database kept working, and
// the `truncated` flag added to explain a short page was never delivered in
// the one case it existed for. Any staff member in no group could trigger it
// by opening the page.
//
// A row cap alone cannot fix that, because cost per row depends on data the
// handler does not control. A budget well under WriteTimeout can: whatever the
// data, the walk stops in time to answer, and says it stopped.
const (
	adminAuditScanCap    = 20000
	adminAuditScanBudget = 5 * time.Second
)

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
//     see every entity filters the result itself. Because of that a staff
//     viewer is given NO total — the pre-narrowing count told them how many
//     entries existed on tickets they could not see, which with this
//     endpoint's filters is an oracle rather than a cosmetic overcount. They
//     get HasMore instead, and their pages are assembled by walking the
//     visible sequence (scopedAuditPage) rather than by passing an offset to
//     a query that does not know about scope.
//     With scope enforcement off every ticket is visible to staff, so this
//     narrowing is a no-op and they get the plain query (plainAuditPage) —
//     still with no total, so staff see one response shape.
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

	var (
		entries   []audit.Entry
		total     *int
		hasMore   bool
		truncated bool
	)

	if role == user.RoleStaff {
		f.EntityType = "ticket"
		var vis ticket.Visibility
		vis, err = s.TicketVisibility(ctx, authmw.GetActor(r))
		switch {
		case err != nil:
		case vis == ticket.VisibilityAll:
			// Scope enforcement off: staff may see every ticket, so every
			// ticket entry is theirs and the plain query is exact. The walk
			// would add a row ceiling and drop entries on tickets that no
			// longer load — limits the admin view does not have, applied to
			// the same entries. Still no total: one response shape for staff.
			entries, hasMore, err = s.plainAuditPage(ctx, f, limit, offset)
		default:
			entries, hasMore, truncated, err = s.scopedAuditPage(r, f, limit, offset)
		}
	} else {
		var n int
		entries, n, err = s.auditStore.Search(ctx, f, limit, offset)
		total = &n
		hasMore = offset+len(entries) < n
	}
	if err != nil {
		handleError(w, err)
		return
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

	JSON(w, http.StatusOK, adminAuditListResponse{
		Entries: views, Total: total, HasMore: hasMore, Truncated: truncated,
	})
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
func (s *Server) filterToVisibleTickets(
	ctx context.Context, canView func(ticket.Ticket) bool, visible map[uuid.UUID]bool, entries []audit.Entry,
) []audit.Entry {
	// visible is owned by the caller and lives for the whole request, not one
	// batch: a walk that resets it per batch re-checks the same ticket every
	// time it reappears. canView is the actor's rule resolved once
	// (ticketViewer) rather than recomputed per ticket.
	kept := make([]audit.Entry, 0, len(entries))
	var orphans int
	for _, e := range entries {
		ok, cached := visible[e.EntityID]
		if !cached {
			t, err := s.tickets.GetByID(ctx, e.EntityID)
			if err != nil {
				// An entry whose ticket is gone is not shown. Counted rather
				// than logged per entry: one request over 20,000 orphans used
				// to write 20,000 warnings.
				orphans++
				visible[e.EntityID] = false
				continue
			}
			ok = canView(t)
			visible[e.EntityID] = ok
		}
		if ok {
			kept = append(kept, e)
		}
	}
	if orphans > 0 {
		slog.WarnContext(ctx, "admin audit: dropped entries whose ticket could not be loaded", "count", orphans)
	}
	return kept
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

// scopedAuditPage assembles one page of a staff viewer's audit entries by
// walking forward over the entries they can actually see.
//
// The offset is counted in VISIBLE entries, not raw rows. That is the whole
// point: scope is applied in Go after the query, so a raw offset indexes a
// different sequence from the one the caller is reading, and paging by it
// returns overlapping pages. Walking costs O(offset) reads, bounded by
// adminAuditScanCap.
//
// Returns hasMore when at least one more visible entry exists beyond the page,
// which it learns by looking one past the end rather than by counting the
// remainder — a count would be the same disclosure Total was removed for.
func (s *Server) scopedAuditPage(r *http.Request, f audit.Filter, limit, offset int) (page []audit.Entry, hasMore, truncated bool, err error) {
	ctx := r.Context()
	canView, err := s.ticketViewer(ctx, authmw.GetActor(r))
	if err != nil {
		return nil, false, false, err
	}
	scanCap, budget, batchSize := s.auditScanLimits()
	deadline := time.Now().Add(budget)
	visible := make(map[uuid.UUID]bool)

	var skipped, scanned int
	var after *audit.Cursor
	for scanned < scanCap && time.Now().Before(deadline) {
		// A client that has gone away should stop the walk, not just the
		// response.
		if err := ctx.Err(); err != nil {
			return nil, false, false, err
		}
		// By position, not offset: an entry written while the walk runs sorts
		// to the front and would shift an offset, so the next batch re-read
		// the previous one's last row. No count either — at a million rows a
		// discarded per-batch count was most of the time budget.
		batch, err := s.auditStore.ListAfter(ctx, f, after, batchSize)
		if err != nil {
			return nil, false, false, err
		}
		scanned += len(batch)
		if len(batch) > 0 {
			last := batch[len(batch)-1]
			after = &audit.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
		}

		for _, e := range s.filterToVisibleTickets(ctx, canView, visible, batch) {
			switch {
			case skipped < offset:
				skipped++
			case len(page) < limit:
				page = append(page, e)
			default:
				// One past the end. Its existence is all the pager needs,
				// and all the caller is told.
				return page, true, false, nil
			}
		}
		// A short batch is the end of the data. Checked here rather than by
		// reading one more empty batch, so that data ending on a batch
		// boundary before the ceiling is reported as complete. Data ending
		// exactly at the ceiling still reports truncated: the last batch is
		// full and the walk cannot tell without reading past the ceiling.
		if len(batch) < batchSize {
			return page, false, false, nil
		}
	}
	// Stopped by the row ceiling or the time budget with data still unread.
	// hasMore stays false because this function cannot serve a next page;
	// truncated says why, so "stopped looking" is not mistaken for "nothing".
	return page, false, true, nil
}

// plainAuditPage is one page of the unscoped query for a staff viewer, with
// has_more from a one-row peek rather than a count.
func (s *Server) plainAuditPage(ctx context.Context, f audit.Filter, limit, offset int) ([]audit.Entry, bool, error) {
	entries, err := s.auditStore.List(ctx, f, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	if len(entries) > limit {
		return entries[:limit], true, nil
	}
	return entries, false, nil
}

// auditScanLimits returns the staff walk's row ceiling, time budget and batch
// size — overridable in tests, where reaching a limit honestly would need tens
// of thousands of rows or a slow database.
func (s *Server) auditScanLimits() (scanCap int, budget time.Duration, batch int) {
	scanCap, budget, batch = adminAuditScanCap, adminAuditScanBudget, adminAuditScanBatch
	if s.auditScanCapOverride > 0 {
		scanCap = s.auditScanCapOverride
	}
	if s.auditScanBudgetOverride > 0 {
		budget = s.auditScanBudgetOverride
	}
	if s.auditScanBatchOverride > 0 {
		batch = s.auditScanBatchOverride
	}
	return scanCap, budget, batch
}

// SetAuditScanLimitsForTest overrides the staff audit walk's limits; a zero
// leaves that limit at its default. Exported for tests only, and named so that
// is unmistakable.
func (s *Server) SetAuditScanLimitsForTest(scanCap int, budget time.Duration, batch int) {
	s.auditScanCapOverride = scanCap
	s.auditScanBudgetOverride = budget
	s.auditScanBatchOverride = batch
}
