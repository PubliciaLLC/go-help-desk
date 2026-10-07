package ticket_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// In-memory doubles for the four interfaces ticket.Service depends on.
//
// The service talks only to interfaces, so none of this needs a database — the
// package had 7.4% coverage not because it was hard to test but because nothing
// had been written. Every double carries an error-injection field so a test can
// make one specific call fail and assert what the service does about it, which
// is the only way to reach the error paths that matter.

// errStoreDown stands in for a transient infrastructure failure.
var errStoreDown = errors.New("connection reset by peer")

// errNotFound is what the doubles return for a miss.
var errNotFound = errors.New("not found")

// ── ticket store ─────────────────────────────────────────────────────────────

type fakeStore struct {
	forUpdateReads    int
	attachmentCreates int

	guestTokens       map[string]guestTokenRow
	errGuestToken     error
	guestTokenCreates int // total CreateGuestToken calls, across rotations

	// onRead rewrites what a read returns, so a test can tell a value that came
	// back from the store apart from the identical-looking one the caller
	// already had in hand.
	onRead func(ticket.Ticket) ticket.Ticket

	tickets map[uuid.UUID]ticket.Ticket
	replies map[uuid.UUID][]ticket.Reply
	history []ticket.StatusHistoryEntry
	links   map[uuid.UUID][]ticket.TicketLink
	seq     int64

	// Call counters, so a test can assert an operation did not write.
	creates        int
	updates        int
	replyCreates   int
	historyCreates int

	// Injectable failures.
	errUpdate        error
	errCreate        error
	errCreateReply   error
	errGetByID       error
	errNextSeq       error
	errCreateHistory error
	errCreateLink    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		tickets: make(map[uuid.UUID]ticket.Ticket),
		replies: make(map[uuid.UUID][]ticket.Reply),
		links:   make(map[uuid.UUID][]ticket.TicketLink),
	}
}

func (f *fakeStore) seed(t ticket.Ticket) { f.tickets[t.ID] = t }

func (f *fakeStore) Create(_ context.Context, t ticket.Ticket) error {
	if f.errCreate != nil {
		return f.errCreate
	}
	f.creates++
	f.tickets[t.ID] = t
	return nil
}

// GetByIDForUpdate is the same read; there is no locking to simulate in a map,
// and the property the lock provides — that lifecycle writes recompute from the
// row they are about to overwrite — is exercised against real Postgres in
// internal/database, where a fake would only assert that a reimplementation
// agrees with itself.
func (f *fakeStore) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (ticket.Ticket, error) {
	f.forUpdateReads++
	return f.GetByID(ctx, id)
}

func (f *fakeStore) GetByID(_ context.Context, id uuid.UUID) (ticket.Ticket, error) {
	if f.errGetByID != nil {
		return ticket.Ticket{}, f.errGetByID
	}
	t, ok := f.tickets[id]
	if !ok {
		return ticket.Ticket{}, errNotFound
	}
	if f.onRead != nil {
		t = f.onRead(t)
	}
	return t, nil
}

func (f *fakeStore) GetByTrackingNumber(_ context.Context, tn ticket.TrackingNumber) (ticket.Ticket, error) {
	for _, t := range f.tickets {
		if t.TrackingNumber == tn {
			return t, nil
		}
	}
	return ticket.Ticket{}, errNotFound
}

func (f *fakeStore) Update(_ context.Context, t ticket.Ticket) error {
	if f.errUpdate != nil {
		return f.errUpdate
	}
	f.updates++
	f.tickets[t.ID] = t
	return nil
}

func (f *fakeStore) UpdateCTI(_ context.Context, id, categoryID uuid.UUID, typeID, itemID *uuid.UUID) error {
	t, ok := f.tickets[id]
	if !ok {
		return errNotFound
	}
	t.CategoryID, t.TypeID, t.ItemID = categoryID, typeID, itemID
	f.tickets[id] = t
	return nil
}

func (f *fakeStore) NextSeq(_ context.Context) (int64, error) {
	if f.errNextSeq != nil {
		return 0, f.errNextSeq
	}
	f.seq++
	return f.seq, nil
}

func (f *fakeStore) CreateReply(_ context.Context, r ticket.Reply) error {
	if f.errCreateReply != nil {
		return f.errCreateReply
	}
	f.replyCreates++
	f.replies[r.TicketID] = append(f.replies[r.TicketID], r)
	return nil
}

func (f *fakeStore) ListReplies(_ context.Context, ticketID uuid.UUID) ([]ticket.Reply, error) {
	return f.replies[ticketID], nil
}

func (f *fakeStore) CreateStatusHistoryEntry(_ context.Context, e ticket.StatusHistoryEntry) error {
	if f.errCreateHistory != nil {
		return f.errCreateHistory
	}
	f.historyCreates++
	f.history = append(f.history, e)
	return nil
}

func (f *fakeStore) ListStatusHistory(_ context.Context, ticketID uuid.UUID) ([]ticket.StatusHistoryEntry, error) {
	var out []ticket.StatusHistoryEntry
	for _, e := range f.history {
		if e.TicketID == ticketID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeStore) CreateLink(_ context.Context, link ticket.TicketLink) error {
	if f.errCreateLink != nil {
		return f.errCreateLink
	}
	// Simulate unique constraint: check if link already exists
	for _, existing := range f.links[link.SourceTicketID] {
		if existing.TargetTicketID == link.TargetTicketID && existing.LinkType == link.LinkType {
			return ticket.ErrLinkAlreadyExists
		}
	}
	f.links[link.SourceTicketID] = append(f.links[link.SourceTicketID], link)
	return nil
}

func (f *fakeStore) DeleteLink(_ context.Context, source, target uuid.UUID, lt ticket.LinkType) error {
	return nil
}

func (f *fakeStore) ListLinks(_ context.Context, ticketID uuid.UUID) ([]ticket.TicketLink, error) {
	return f.links[ticketID], nil
}

// Listings and search are not exercised by these tests; they exist to satisfy
// the interface and return empty rather than pretending to filter.
func (f *fakeStore) ListByReporter(context.Context, uuid.UUID, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}
func (f *fakeStore) ListByAssigneeUser(context.Context, uuid.UUID, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}
func (f *fakeStore) ListByAssigneeGroup(context.Context, uuid.UUID, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}
func (f *fakeStore) ListByStatus(context.Context, uuid.UUID, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}
func (f *fakeStore) ListAll(context.Context, int, int) ([]ticket.Ticket, error) { return nil, nil }
func (f *fakeStore) ListUnassigned(context.Context, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}

// IsAssignableUser and IsAssignableGroup: this fake has no user table, so
// everything it is asked about is assignable. The real rule lives in SQL and
// is exercised against a real database.
func (f *fakeStore) IsAssignableUser(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}

func (f *fakeStore) CTIIsCoherent(context.Context, uuid.UUID, *uuid.UUID, *uuid.UUID) (bool, error) {
	return true, nil
}

func (f *fakeStore) CategoryExists(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}

func (f *fakeStore) UserExists(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}

func (f *fakeStore) IsAssignableGroup(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}

// UnassignForUser clears the assignee on every open ticket held by a user.
func (f *fakeStore) UnassignForUser(_ context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	var moved []uuid.UUID
	for id, t := range f.tickets {
		if t.AssigneeUserID == nil || *t.AssigneeUserID != userID {
			continue
		}
		if t.ResolvedAt != nil || t.ClosedAt != nil {
			continue
		}
		t.AssigneeUserID = nil
		f.tickets[id] = t
		moved = append(moved, id)
	}
	return moved, nil
}

func (f *fakeStore) ListResolvedBefore(_ context.Context, before time.Time, resolvedStatusID uuid.UUID, limit int) ([]ticket.Ticket, error) {
	// Filter: ResolvedAt != nil && ResolvedAt < before && StatusID == resolvedStatusID && ClosedAt == nil
	// Sort by ResolvedAt ascending, apply limit.
	//
	// StatusID is checked here to mirror the real query's `status_id = $2`
	// (#191): a row with a stale resolved_at that has since moved to a
	// different status must not be listed, or the sweep would relock and skip
	// it forever instead of it simply falling out of the candidate set.
	var candidates []ticket.Ticket
	for _, t := range f.tickets {
		if t.ResolvedAt != nil && t.ResolvedAt.Before(before) && t.StatusID == resolvedStatusID && t.ClosedAt == nil {
			candidates = append(candidates, t)
		}
	}
	// Sort by ResolvedAt ascending (earliest first).
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].ResolvedAt.Before(*candidates[i].ResolvedAt) {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}
func (f *fakeStore) SearchByReporter(context.Context, uuid.UUID, string, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}
func (f *fakeStore) SearchByAssigneeUser(context.Context, uuid.UUID, string, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}
func (f *fakeStore) SearchByAssigneeGroup(context.Context, uuid.UUID, string, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}
func (f *fakeStore) SearchAll(context.Context, string, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}
func (f *fakeStore) SearchUnassigned(context.Context, string, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}

// Scope-aware listing is exercised against real Postgres in the server suite,
// where the SQL predicate is the thing under test. A Go reimplementation here
// would assert that the fake matches itself.
func (f *fakeStore) ListVisibleToStaff(context.Context, uuid.UUID, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}

func (f *fakeStore) ListFiltered(context.Context, ticket.Filter) ([]ticket.Ticket, error) {
	return nil, nil
}

func (f *fakeStore) SearchVisibleToStaff(context.Context, uuid.UUID, string, int, int) ([]ticket.Ticket, error) {
	return nil, nil
}
func (f *fakeStore) CreateAttachment(context.Context, ticket.Attachment) error {
	f.attachmentCreates++
	return nil
}
func (f *fakeStore) GetAttachmentByID(context.Context, uuid.UUID) (ticket.Attachment, error) {
	return ticket.Attachment{}, errNotFound
}
func (f *fakeStore) ListAttachments(context.Context, uuid.UUID) ([]ticket.Attachment, error) {
	return nil, nil
}
func (f *fakeStore) DeleteAttachment(context.Context, uuid.UUID) error { return nil }

// ── status store ─────────────────────────────────────────────────────────────

type fakeStatusStore struct {
	historyByStatus map[uuid.UUID]int64
	byName          map[string]ticket.Status

	// counts[statusID] is how many tickets sit on that status, so a test can
	// make RemoveStatus see a status that is in use.
	counts map[uuid.UUID]int64

	deletes int
	updates int
}

func (f *fakeStatusStore) GetStatusByName(_ context.Context, name string) (ticket.Status, error) {
	s, ok := f.byName[name]
	if !ok {
		return ticket.Status{}, errNotFound
	}
	return s, nil
}

func (f *fakeStatusStore) ListStatuses(context.Context) ([]ticket.Status, error) {
	out := make([]ticket.Status, 0, len(f.byName))
	for _, s := range f.byName {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeStatusStore) CreateStatus(context.Context, ticket.Status) error { return nil }
func (f *fakeStatusStore) UpdateStatus(context.Context, ticket.Status) error {
	f.updates++
	return nil
}
func (f *fakeStatusStore) DeleteStatus(context.Context, uuid.UUID) error {
	f.deletes++
	return nil
}

// historyByStatus lets a test say a status is referenced by past transitions.
func (f *fakeStatusStore) CountStatusHistoryByStatus(_ context.Context, id uuid.UUID) (int64, error) {
	return f.historyByStatus[id], nil
}

func (f *fakeStatusStore) CountByStatus(_ context.Context, id uuid.UUID) (int64, error) {
	return f.counts[id], nil
}
func (f *fakeStatusStore) CountByStatusForReporter(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *fakeStatusStore) CountByStatusForAssignee(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) (int64, error) {
	return 0, nil
}

// ── dispatcher, audit, SLA ───────────────────────────────────────────────────

type fakeDispatcher struct {
	events []notification.Event
	err    error
	// sendTime stands in for the outbox worker's send-time step (#164): the
	// harness sets it to Service.IssueGuestLink, so an event marked GuestLink
	// is recorded as it would be sent — carrying the token the send created,
	// or none if nobody is left to send it to. Rotation tests keep asserting
	// on GuestToken, now created at send rather than at the change.
	sendTime func(context.Context, notification.Event) (notification.Event, bool, error)
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, e notification.Event) error {
	if f.err != nil {
		return f.err
	}
	if e.GuestLink && f.sendTime != nil {
		// A send-time failure does not undo the queueing, which succeeded;
		// the worker retries it. Record the event as queued.
		if sent, ok, err := f.sendTime(ctx, e); err == nil && ok {
			e = sent
		}
	}
	f.events = append(f.events, e)
	return nil
}

// types reports the event types dispatched, in order, so a test can assert that
// no notification was sent for a state change that failed to persist.
func (f *fakeDispatcher) types() []notification.EventType {
	out := make([]notification.EventType, 0, len(f.events))
	for _, e := range f.events {
		out = append(out, e.Type)
	}
	return out
}

type fakeAuditStore struct {
	entries []audit.Entry
	err     error
}

func (f *fakeAuditStore) Create(_ context.Context, e audit.Entry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, e)
	return nil
}

// ListByEntity mirrors the real store's query closely enough to be a
// meaningful double: filtered to the (entityType, entityID) pair, newest
// first, limit/offset applied — not a stub that always returns nothing.
func (f *fakeAuditStore) ListByEntity(_ context.Context, entityType string, entityID uuid.UUID, limit, offset int) ([]audit.Entry, error) {
	var matched []audit.Entry
	for _, e := range f.entries {
		if e.EntityType == entityType && e.EntityID == entityID {
			matched = append(matched, e)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })
	if offset >= len(matched) {
		return nil, nil
	}
	matched = matched[offset:]
	if limit > 0 && limit < len(matched) {
		matched = matched[:limit]
	}
	return matched, nil
}

// Search mirrors the real store's filter semantics closely enough to be a
// meaningful double: every Filter field is optional and narrows the result,
// newest first, with the total count taken before limit/offset is applied.
func (f *fakeAuditStore) Search(_ context.Context, filter audit.Filter, limit, offset int) ([]audit.Entry, int, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	// Scope is a query's job and this double has no ticket table to apply it
	// to; ignoring it would hand back entries the caller asked to have hidden.
	if filter.ScopedTo != nil {
		return nil, 0, errors.New("fakeAuditStore does not implement Filter.ScopedTo")
	}
	var matched []audit.Entry
	for _, e := range f.entries {
		if filter.EntityType != "" && e.EntityType != filter.EntityType {
			continue
		}
		if filter.Action != "" && e.Action != filter.Action {
			continue
		}
		if filter.ActorID != nil && (e.ActorID == nil || *e.ActorID != *filter.ActorID) {
			continue
		}
		if filter.From != nil && e.CreatedAt.Before(*filter.From) {
			continue
		}
		if filter.To != nil && e.CreatedAt.After(*filter.To) {
			continue
		}
		if filter.Q != "" && !strings.Contains(e.EntityType, filter.Q) && !strings.Contains(e.Action, filter.Q) {
			continue
		}
		matched = append(matched, e)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })
	total := len(matched)
	if offset >= len(matched) {
		return nil, total, nil
	}
	matched = matched[offset:]
	if limit > 0 && limit < len(matched) {
		matched = matched[:limit]
	}
	return matched, total, nil
}

func (f *fakeAuditStore) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	var kept []audit.Entry
	var deleted int64
	for _, e := range f.entries {
		if e.CreatedAt.Before(cutoff) {
			deleted++
			continue
		}
		kept = append(kept, e)
	}
	f.entries = kept
	return deleted, nil
}

type fakeSLA struct {
	firstResponses int
	resolutions    int
	err            error

	// lastResolvedAt is the `at` argument RecordResolved was last called
	// with, so a test can assert WHICH instant close()/UpdateStatus recorded
	// a resolution against — the real ticket's own ResolvedAt, not a bare
	// close-time now(). See #227.
	lastResolvedAt time.Time
}

func (f *fakeSLA) AttachPolicy(context.Context, ticket.Ticket) error { return nil }

func (f *fakeSLA) RecordResolved(_ context.Context, _ ticket.Ticket, at time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.resolutions++
	f.lastResolvedAt = at
	return nil
}

func (f *fakeSLA) RecordFirstResponse(_ context.Context, _ ticket.Ticket, _ time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.firstResponses++
	return nil
}

// ── transactions ─────────────────────────────────────────────────────────────

// fakeAtomic implements ticket.Atomic with real rollback semantics: it
// snapshots the stores before running fn and restores them if fn fails.
//
// A double that simply called fn would test nothing about atomicity — every
// assertion that a failure leaves no trace would pass whether the production
// code used a transaction or not. Undoing the writes is what makes those
// assertions mean something.
type fakeAtomic struct {
	store *fakeStore
	audit *fakeAuditStore

	commits    int
	rollbacks  int
	errBeginTx error
}

func (a *fakeAtomic) InTx(_ context.Context, fn func(ticket.Store, audit.Store) error) error {
	if a.errBeginTx != nil {
		return a.errBeginTx
	}

	// Snapshot. The maps are shallow-copied and the slices cloned, which is
	// enough because the service replaces whole records rather than mutating
	// them in place.
	tickets := make(map[uuid.UUID]ticket.Ticket, len(a.store.tickets))
	for k, v := range a.store.tickets {
		tickets[k] = v
	}
	replies := make(map[uuid.UUID][]ticket.Reply, len(a.store.replies))
	for k, v := range a.store.replies {
		replies[k] = append([]ticket.Reply(nil), v...)
	}
	history := append([]ticket.StatusHistoryEntry(nil), a.store.history...)
	entries := append([]audit.Entry(nil), a.audit.entries...)
	links := make(map[uuid.UUID][]ticket.TicketLink, len(a.store.links))
	for k, v := range a.store.links {
		links[k] = append([]ticket.TicketLink(nil), v...)
	}
	counters := [4]int{a.store.creates, a.store.updates, a.store.replyCreates, a.store.historyCreates}

	if err := fn(a.store, a.audit); err != nil {
		a.store.tickets = tickets
		a.store.replies = replies
		a.store.history = history
		a.audit.entries = entries
		a.store.links = links
		a.store.creates, a.store.updates, a.store.replyCreates, a.store.historyCreates =
			counters[0], counters[1], counters[2], counters[3]
		a.rollbacks++
		return err
	}

	a.commits++
	return nil
}

// ── guest access tokens ──────────────────────────────────────────────────────
//
// Keyed by hash, exactly as the real store is: a test that could look a token
// up by its raw value would not notice the day the hashing stopped happening.

type guestTokenRow struct {
	ticketID  uuid.UUID
	expiresAt time.Time
	usedAt    *time.Time
}

func (f *fakeStore) CreateGuestToken(_ context.Context, _, ticketID uuid.UUID, hash string, expiresAt time.Time) error {
	if f.errGuestToken != nil {
		return f.errGuestToken
	}
	if f.guestTokens == nil {
		f.guestTokens = map[string]guestTokenRow{}
	}
	f.guestTokens[hash] = guestTokenRow{ticketID: ticketID, expiresAt: expiresAt}
	f.guestTokenCreates++
	return nil
}

func (f *fakeStore) TicketByGuestToken(ctx context.Context, hash string) (ticket.Ticket, error) {
	row, ok := f.guestTokens[hash]
	// Expiry decided here, as the query decides it, so a test cannot pass
	// against a fake that is more permissive than the database. Closure is
	// NOT decided here (#349): a closed ticket resolves, and the service
	// refuses it for writes.
	if !ok || !row.expiresAt.After(time.Now()) {
		return ticket.Ticket{}, ticket.ErrGuestTokenNotFound
	}
	t, err := f.GetByID(ctx, row.ticketID)
	if err != nil {
		return ticket.Ticket{}, ticket.ErrGuestTokenNotFound
	}
	return t, nil
}

func (f *fakeStore) TouchGuestToken(_ context.Context, hash string) error {
	row, ok := f.guestTokens[hash]
	if !ok || row.usedAt != nil {
		return nil
	}
	now := time.Now()
	row.usedAt = &now
	f.guestTokens[hash] = row
	return nil
}

func (f *fakeStore) DeleteGuestTokensForTicket(_ context.Context, ticketID uuid.UUID) error {
	for h, row := range f.guestTokens {
		if row.ticketID == ticketID {
			delete(f.guestTokens, h)
		}
	}
	return nil
}

func (f *fakeStore) TicketIDByTrackingAndGuestEmail(_ context.Context, tn ticket.TrackingNumber, email string) (uuid.UUID, error) {
	for _, t := range f.tickets {
		if t.TrackingNumber == tn && t.GuestEmail != nil &&
			strings.EqualFold(*t.GuestEmail, email) {
			return t.ID, nil
		}
	}
	return uuid.Nil, ticket.ErrGuestTokenNotFound
}

// expireGuestTokens backdates every token a ticket holds, as thirty days
// passing would.
func (f *fakeStore) expireGuestTokens(ticketID uuid.UUID) {
	for h, row := range f.guestTokens {
		if row.ticketID == ticketID {
			row.expiresAt = time.Now().Add(-time.Minute)
			f.guestTokens[h] = row
		}
	}
}

// latestGuestTokenExpiry is the furthest expiry among a ticket's tokens.
func (f *fakeStore) latestGuestTokenExpiry(ticketID uuid.UUID) time.Time {
	var latest time.Time
	for _, row := range f.guestTokens {
		if row.ticketID == ticketID && row.expiresAt.After(latest) {
			latest = row.expiresAt
		}
	}
	return latest
}

// guestTokenCount reports how many tokens a ticket holds, so a test can assert
// that rotation replaced rather than accumulated.
func (f *fakeStore) guestTokenCount(ticketID uuid.UUID) int {
	n := 0
	for _, row := range f.guestTokens {
		if row.ticketID == ticketID {
			n++
		}
	}
	return n
}
