package ticket_test

import (
	"context"
	"errors"
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

func (f *fakeStore) GetByID(_ context.Context, id uuid.UUID) (ticket.Ticket, error) {
	if f.errGetByID != nil {
		return ticket.Ticket{}, f.errGetByID
	}
	t, ok := f.tickets[id]
	if !ok {
		return ticket.Ticket{}, errNotFound
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
func (f *fakeStore) ListResolvedBefore(context.Context, time.Time, int) ([]ticket.Ticket, error) {
	return nil, nil
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
func (f *fakeStore) CreateAttachment(context.Context, ticket.Attachment) error { return nil }
func (f *fakeStore) GetAttachmentByID(context.Context, uuid.UUID) (ticket.Attachment, error) {
	return ticket.Attachment{}, errNotFound
}
func (f *fakeStore) ListAttachments(context.Context, uuid.UUID) ([]ticket.Attachment, error) {
	return nil, nil
}
func (f *fakeStore) DeleteAttachment(context.Context, uuid.UUID) error { return nil }

// ── status store ─────────────────────────────────────────────────────────────

type fakeStatusStore struct {
	byName map[string]ticket.Status

	// counts[statusID] is how many tickets sit on that status, so a test can
	// make RemoveStatus see a status that is in use.
	counts map[uuid.UUID]int64

	deletes int
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
func (f *fakeStatusStore) UpdateStatus(context.Context, ticket.Status) error { return nil }
func (f *fakeStatusStore) DeleteStatus(context.Context, uuid.UUID) error {
	f.deletes++
	return nil
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
}

func (f *fakeDispatcher) Dispatch(_ context.Context, e notification.Event) error {
	if f.err != nil {
		return f.err
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

func (f *fakeAuditStore) ListByEntity(context.Context, string, uuid.UUID, int, int) ([]audit.Entry, error) {
	return nil, nil
}

type fakeSLA struct {
	firstResponses int
	err            error
}

func (f *fakeSLA) AttachPolicy(context.Context, ticket.Ticket) error { return nil }

func (f *fakeSLA) RecordFirstResponse(_ context.Context, _ uuid.UUID, _ time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.firstResponses++
	return nil
}
