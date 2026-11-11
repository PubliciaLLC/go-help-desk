package sla_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// RecordFirstResponse and RecordResolved against a re-match that lands between
// their read of the record and their write (#623). The real-Postgres versions,
// which make the same interleavings happen with real locks, are in
// internal/database/sla_record_race_test.go; these pin the service's own
// decisions: retry on a superseded record, give up after three, and never put
// a reading from before a restart on the new clock (D17).

// reMatchingStore runs onPolicyRead on every GetPolicy, which the service calls
// between reading the record and writing it, and counts the reads.
type reMatchingStore struct {
	*fakeSLAStore
	onPolicyRead func(read int)
	recordReads  int
}

func (s *reMatchingStore) GetRecord(ctx context.Context, id uuid.UUID) (sla.Record, error) {
	s.recordReads++
	return s.fakeSLAStore.GetRecord(ctx, id)
}

func (s *reMatchingStore) GetPolicy(ctx context.Context, id uuid.UUID) (sla.Policy, error) {
	p, err := s.fakeSLAStore.GetPolicy(ctx, id) // the read the service asked for, before the re-match
	if s.onPolicyRead != nil {
		s.onPolicyRead(s.recordReads)
	}
	return p, err
}

// replaceRecord is a re-match: the ticket's record is superseded and a new one,
// with a new id, takes its place. A nil restartAt keeps the clock and the
// facts, as a staff re-match does; a restartAt is a reopen: a new clock and
// no facts.
func replaceRecord(store *fakeSLAStore, ticketID, policyID uuid.UUID, restartAt *time.Time) sla.Record {
	next := sla.Record{ID: uuid.New(), TicketID: ticketID, PolicyID: policyID}
	if restartAt == nil {
		old := store.records[ticketID]
		next.StartedAt, next.PausedSecondsAtStart = old.StartedAt, old.PausedSecondsAtStart
		next.FirstResponseAt, next.ResolvedAt = old.FirstResponseAt, old.ResolvedAt
	} else {
		next.StartedAt = restartAt
	}
	store.records[ticketID] = next
	return next
}

// raceWrite is one of the two writes, with the reading of its fact and its
// breach stamp off a record.
type raceWrite struct {
	name   string
	run    func(*sla.Service, ticket.Ticket, time.Time) error
	fact   func(sla.Record) *time.Time
	breach func(sla.Record) *time.Time
	calls  func(*fakeSLAStore) int
}

var raceWrites = []raceWrite{
	{
		name: "RecordFirstResponse",
		run: func(s *sla.Service, t ticket.Ticket, at time.Time) error {
			return s.RecordFirstResponse(context.Background(), t, at)
		},
		fact:   func(r sla.Record) *time.Time { return r.FirstResponseAt },
		breach: func(r sla.Record) *time.Time { return r.ResponseBreachedAt },
		calls:  func(f *fakeSLAStore) int { return f.setFirstResponseCalls },
	},
	{
		name: "RecordResolved",
		run: func(s *sla.Service, t ticket.Ticket, at time.Time) error {
			return s.RecordResolved(context.Background(), t, at)
		},
		fact:   func(r sla.Record) *time.Time { return r.ResolvedAt },
		breach: func(r sla.Record) *time.Time { return r.ResolutionBreachedAt },
		calls:  func(f *fakeSLAStore) int { return f.setResolvedAndFirstResponseCalls },
	},
}

// raceFixture is a ticket three hours old with a record under a policy it is
// nowhere near breaching, and a second, strict policy a re-match can move it to.
func raceFixture(t *testing.T) (store *reMatchingStore, tk ticket.Ticket, lenient, strict sla.Policy, at time.Time) {
	t.Helper()
	store = &reMatchingStore{fakeSLAStore: newFakeSLAStore()}
	lenient = sla.Policy{ID: uuid.New(), ResponseTargetMin: 6000, ResolutionTargetMin: 12000}
	strict = sla.Policy{ID: uuid.New(), ResponseTargetMin: 10, ResolutionTargetMin: 20}
	store.policies[lenient.ID], store.policies[strict.ID] = lenient, strict
	at = time.Now().Truncate(time.Millisecond)
	tk = ticket.Ticket{ID: uuid.New(), CreatedAt: at.Add(-3 * time.Hour)}
	store.records[tk.ID] = sla.Record{ID: uuid.New(), TicketID: tk.ID, PolicyID: lenient.ID}
	return store, tk, lenient, strict, at
}

// The record was superseded by a staff re-match after it was read: the write is
// retried on the record that replaced it, computed from that record's policy.
func TestRecordWrite_RetriesOnTheRecordThatReplacedIt(t *testing.T) {
	for _, w := range raceWrites {
		t.Run(w.name, func(t *testing.T) {
			store, tk, _, strict, at := raceFixture(t)
			var next sla.Record
			store.onPolicyRead = func(read int) {
				if read == 1 {
					next = replaceRecord(store.fakeSLAStore, tk.ID, strict.ID, nil)
				}
			}

			require.NoError(t, w.run(sla.NewService(store), tk, at))

			cur := store.records[tk.ID]
			require.Equal(t, next.ID, cur.ID)
			require.NotNil(t, w.fact(cur), "the fact is on the record that is current")
			require.True(t, w.fact(cur).Equal(at))
			require.NotNil(t, w.breach(cur), "judged against the new policy, whose target three hours exceeds")
			require.Equal(t, 2, w.calls(store.fakeSLAStore), "one write refused, one landed")
			require.Equal(t, 2, store.recordReads)
		})
	}
}

// A restart replaced the record after it was read. The reading is from before
// the restart, so it is dropped rather than recorded on the new clock (D17),
// and that is not an error.
func TestRecordWrite_ARestartDropsAReadingFromBeforeIt(t *testing.T) {
	for _, w := range raceWrites {
		t.Run(w.name, func(t *testing.T) {
			store, tk, _, strict, at := raceFixture(t)
			restart := at.Add(time.Second)
			var next sla.Record
			store.onPolicyRead = func(read int) {
				if read == 1 {
					next = replaceRecord(store.fakeSLAStore, tk.ID, strict.ID, &restart)
				}
			}

			require.NoError(t, w.run(sla.NewService(store), tk, at))

			cur := store.records[tk.ID]
			require.Equal(t, next.ID, cur.ID)
			require.Nil(t, w.fact(cur), "the new clock has no fact from before it started")
			require.Nil(t, w.breach(cur))
			require.Equal(t, 1, w.calls(store.fakeSLAStore), "only the write that found the record superseded")
		})
	}
}

// The same rule when the restart is already in place on the first read: nothing
// is written at all.
func TestRecordWrite_AReadingFromBeforeTheRecordStartedIsNeverWritten(t *testing.T) {
	for _, w := range raceWrites {
		t.Run(w.name, func(t *testing.T) {
			store, tk, _, _, at := raceFixture(t)
			started := at.Add(time.Second)
			rec := store.records[tk.ID]
			rec.StartedAt = &started
			store.records[tk.ID] = rec

			require.NoError(t, w.run(sla.NewService(store), tk, at))

			require.Zero(t, w.calls(store.fakeSLAStore))
			require.Nil(t, w.fact(store.records[tk.ID]))
		})
	}
}

// An instant equal to the record's start is on the new clock: a reply stamped
// the same instant the clock started is not "before" it.
func TestRecordWrite_AReadingAtTheRecordStartCounts(t *testing.T) {
	for _, w := range raceWrites {
		t.Run(w.name, func(t *testing.T) {
			store, tk, _, _, at := raceFixture(t)
			rec := store.records[tk.ID]
			rec.StartedAt = &at
			store.records[tk.ID] = rec

			require.NoError(t, w.run(sla.NewService(store), tk, at))

			require.NotNil(t, w.fact(store.records[tk.ID]))
		})
	}
}

// A ticket that is re-matched every time it is read is not retried forever.
// Three attempts, then an error that names the cause, for the boundary to log.
func TestRecordWrite_GivesUpAfterThreeAttempts(t *testing.T) {
	for _, w := range raceWrites {
		t.Run(w.name, func(t *testing.T) {
			store, tk, lenient, strict, at := raceFixture(t)
			flip := []uuid.UUID{strict.ID, lenient.ID}
			store.onPolicyRead = func(read int) {
				replaceRecord(store.fakeSLAStore, tk.ID, flip[read%2], nil)
			}

			err := w.run(sla.NewService(store), tk, at)

			require.ErrorIs(t, err, sla.ErrRecordSuperseded)
			require.Equal(t, 3, store.recordReads)
			require.Equal(t, 3, w.calls(store.fakeSLAStore))
			require.Nil(t, w.fact(store.records[tk.ID]))
		})
	}
}

// The ticket lost its record entirely between the read and the write (the
// re-match removed it: the new priority has no policy): that is "not under an
// SLA", not an error.
func TestRecordWrite_ARecordRemovedMeanwhileIsNotAnError(t *testing.T) {
	for _, w := range raceWrites {
		t.Run(w.name, func(t *testing.T) {
			store, tk, _, _, at := raceFixture(t)
			store.onPolicyRead = func(read int) { delete(store.records, tk.ID) }

			require.NoError(t, w.run(sla.NewService(store), tk, at))
		})
	}
}
