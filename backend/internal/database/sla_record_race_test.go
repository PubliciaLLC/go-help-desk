package database_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/slastore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// RecordFirstResponse and RecordResolved racing a re-match, against real
// Postgres (#623). Both read the ticket's current SLA record, compute from it,
// and write. A re-match (priority change, reopen restart) can commit between
// the read and the write, in two orders, and the tests make each happen on
// purpose rather than hoping a stress loop finds it:
//
//   - the re-match has locked the record but not yet committed: the write
//     queues behind its lock, then finds the record superseded (held open with
//     a real row lock, released only once the write is seen waiting on it);
//   - the re-match commits before the write is issued: the write now meets the
//     NEW record, with a reading computed from the old one (a hook between the
//     service's read and its write commits the re-match).
//
// Neither uses a sleep to produce the order.

// recordIDOf is the id of the ticket's current record. The SLA store's record
// writes are addressed by it, not by ticket (#623).
func recordIDOf(t *testing.T, ctx context.Context, ss *slastore.Store, ticketID uuid.UUID) uuid.UUID {
	t.Helper()
	rec, err := ss.GetRecord(ctx, ticketID)
	require.NoError(t, err)
	return rec.ID
}

// reMatch is the SQL a re-match runs, left uncommitted: it locks the ticket's
// current record, supersedes it and starts a new one under policyID. A nil
// restartAt is a staff re-match, which carries the old record's facts onto the
// new one and keeps its clock. A restartAt is a reopen: the clock starts there
// and no facts are carried.
func (f *changePriorityFx) reMatch(t *testing.T, ticketID, policyID uuid.UUID, restartAt *time.Time) (tx *sql.Tx, newID uuid.UUID) {
	t.Helper()
	tx, err := f.db.SQL.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	q := dbgen.New(tx)
	cur, err := q.GetCurrentSLARecordForUpdate(f.ctx, ticketID)
	require.NoError(t, err)
	n, err := q.SupersedeSLARecord(f.ctx, dbgen.SupersedeSLARecordParams{ID: cur.ID, SupersededAt: time.Now().UTC()})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	newID = uuid.New()
	p := dbgen.InsertSLARecordParams{ID: newID, TicketID: ticketID, PolicyID: policyID}
	if restartAt != nil {
		p.StartedAt = sql.NullTime{Time: *restartAt, Valid: true}
	} else {
		p.StartedAt, p.PausedSecondsAtStart = cur.StartedAt, cur.PausedSecondsAtStart
		p.FirstResponseAt, p.ResolvedAt = cur.FirstResponseAt, cur.ResolvedAt
		p.ResponseBreachedAt, p.ResolutionBreachedAt = cur.ResponseBreachedAt, cur.ResolutionBreachedAt
		p.ResponseElapsedAtMetSeconds, p.ResolutionElapsedAtMetSeconds = cur.ResponseElapsedAtMetSeconds, cur.ResolutionElapsedAtMetSeconds
	}
	require.NoError(t, q.InsertSLARecord(f.ctx, p))
	return tx, newID
}

// reMatchNow is reMatch, committed.
func (f *changePriorityFx) reMatchNow(t *testing.T, ticketID, policyID uuid.UUID, restartAt *time.Time) uuid.UUID {
	t.Helper()
	tx, id := f.reMatch(t, ticketID, policyID, restartAt)
	require.NoError(t, tx.Commit())
	return id
}

// waitBlockedOn returns once some backend is waiting on a lock held by tx. It
// asks Postgres who blocks whom, so a lock wait from an unrelated test sharing
// the database cannot satisfy it.
func (f *changePriorityFx) waitBlockedOn(t *testing.T, tx *sql.Tx) {
	t.Helper()
	var pid int
	require.NoError(t, tx.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		require.NoError(t, f.db.SQL.QueryRowContext(f.ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, pid).Scan(&n))
		if n > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond) // polling a condition, not ordering the test
	}
	t.Fatal("no write ever queued behind the re-match's lock")
}

// interposeStore runs hook once, after the SLA service has read the record and
// its policy and before it writes: the service calls GetPolicy between the two.
type interposeStore struct {
	sla.Store
	once sync.Once
	hook func()
}

func (s *interposeStore) GetPolicy(ctx context.Context, id uuid.UUID) (sla.Policy, error) {
	s.once.Do(s.hook)
	return s.Store.GetPolicy(ctx, id)
}

// racePolicies returns two policies for the fixture's category: one whose
// response target the ticket (3 hours old) has comfortably not reached, one
// whose target it has long passed. Which one a write is judged against decides
// whether a breach is stamped.
func (f *changePriorityFx) racePolicies(t *testing.T) (lenient, strict sla.Policy) {
	t.Helper()
	mk := func(name string, respMin, resMin int) sla.Policy {
		p := sla.Policy{ID: uuid.New(), Name: name, CategoryID: &f.tk.CategoryID, ResponseTargetMin: respMin, ResolutionTargetMin: resMin}
		require.NoError(t, f.ss.CreatePolicy(f.ctx, p))
		return p
	}
	return mk("race lenient", 6000, 12000), mk("race strict", 10, 20)
}

func (f *changePriorityFx) currentOf(t *testing.T, id uuid.UUID) dbgen.SlaRecord {
	t.Helper()
	_, current := f.recordsOf(t, id)
	require.Len(t, current, 1, "exactly one current record")
	return current[0]
}

// slaWrite is one of the two writes under test.
type slaWrite struct {
	name string
	run  func(*sla.Service, ticket.Ticket, time.Time) error
	// at reports the fact this write sets on a record.
	at func(dbgen.SlaRecord) sql.NullTime
	// breach reports the breach stamp that fact decides.
	breach func(dbgen.SlaRecord) sql.NullTime
}

var slaWrites = []slaWrite{
	{
		name: "first response",
		run: func(s *sla.Service, tk ticket.Ticket, at time.Time) error {
			return s.RecordFirstResponse(context.Background(), tk, at)
		},
		at:     func(r dbgen.SlaRecord) sql.NullTime { return r.FirstResponseAt },
		breach: func(r dbgen.SlaRecord) sql.NullTime { return r.ResponseBreachedAt },
	},
	{
		name: "resolved",
		run: func(s *sla.Service, tk ticket.Ticket, at time.Time) error {
			return s.RecordResolved(context.Background(), tk, at)
		},
		at:     func(r dbgen.SlaRecord) sql.NullTime { return r.ResolvedAt },
		breach: func(r dbgen.SlaRecord) sql.NullTime { return r.ResolutionBreachedAt },
	},
}

// The write is issued, queues behind a staff re-match that has locked and
// superseded the record, and finds it superseded when the lock is released.
// Before #623 it updated nothing, and the response vanished: the new record had
// been copied from the old one before the response was on it.
func TestSLARecordRace_WriteQueuedBehindAStaffReMatchIsNotLost(t *testing.T) {
	for _, w := range slaWrites {
		t.Run(w.name, func(t *testing.T) {
			f := newChangePriorityFx(t)
			lenient, strict := f.racePolicies(t)
			tk := f.tk
			require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: lenient.ID}))
			old := f.currentOf(t, tk.ID)
			svc := sla.NewService(f.ss)

			tx, newID := f.reMatch(t, tk.ID, strict.ID, nil)
			at := time.Now().UTC().Truncate(time.Millisecond)
			done := make(chan error, 1)
			go func() { done <- w.run(svc, tk, at) }()
			f.waitBlockedOn(t, tx)
			require.NoError(t, tx.Commit())
			require.NoError(t, <-done)

			cur := f.currentOf(t, tk.ID)
			require.Equal(t, newID, cur.ID)
			require.NotEqual(t, old.ID, cur.ID)
			require.True(t, w.at(cur).Valid, "the fact is on the current record")
			require.True(t, w.at(cur).Time.Equal(at))
			require.True(t, w.breach(cur).Valid,
				"and it was judged against the policy the ticket is under now, whose target it has passed")
		})
	}
}

// The re-match is a reopen, which starts the clock over: the write queued
// behind it carries an instant from before the restart, and a response from
// before a reopen never counts on the new clock (D17).
func TestSLARecordRace_WriteQueuedBehindARestartIsDropped(t *testing.T) {
	for _, w := range slaWrites {
		t.Run(w.name, func(t *testing.T) {
			f := newChangePriorityFx(t)
			lenient, strict := f.racePolicies(t)
			tk := f.tk
			require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: lenient.ID}))
			svc := sla.NewService(f.ss)

			at := time.Now().UTC().Truncate(time.Millisecond)
			restart := at.Add(time.Second)
			tx, newID := f.reMatch(t, tk.ID, strict.ID, &restart)
			done := make(chan error, 1)
			go func() { done <- w.run(svc, tk, at) }()
			f.waitBlockedOn(t, tx)
			require.NoError(t, tx.Commit())
			require.NoError(t, <-done)

			cur := f.currentOf(t, tk.ID)
			require.Equal(t, newID, cur.ID)
			require.False(t, w.at(cur).Valid, "the new clock has no fact from before it started")
			require.False(t, w.breach(cur).Valid)
		})
	}
}

// The re-match commits between the service's read and its write. The write used
// to be keyed on the ticket, so it landed on the new record, with the reading
// and the breach decision from the old record's clock and policy.
func TestSLARecordRace_ReMatchCommittingBetweenReadAndWrite(t *testing.T) {
	for _, w := range slaWrites {
		t.Run(w.name+"/staff re-match", func(t *testing.T) {
			f := newChangePriorityFx(t)
			lenient, strict := f.racePolicies(t)
			tk := f.tk
			require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: lenient.ID}))
			var newID uuid.UUID
			store := &interposeStore{Store: f.ss, hook: func() { newID = f.reMatchNow(t, tk.ID, strict.ID, nil) }}

			at := time.Now().UTC().Truncate(time.Millisecond)
			require.NoError(t, w.run(sla.NewService(store), tk, at))

			cur := f.currentOf(t, tk.ID)
			require.Equal(t, newID, cur.ID, "the hook ran: the record changed under the service")
			require.True(t, w.at(cur).Valid && w.at(cur).Time.Equal(at), "the fact lands on the record that is current")
			require.True(t, w.breach(cur).Valid,
				"judged against the new policy (target passed), not the one the service first read (target not reached)")
		})

		t.Run(w.name+"/reopen restart", func(t *testing.T) {
			f := newChangePriorityFx(t)
			lenient, strict := f.racePolicies(t)
			tk := f.tk
			require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: lenient.ID}))
			at := time.Now().UTC().Truncate(time.Millisecond)
			var newID uuid.UUID
			store := &interposeStore{Store: f.ss, hook: func() {
				restart := at.Add(time.Second)
				newID = f.reMatchNow(t, tk.ID, strict.ID, &restart)
			}}

			require.NoError(t, w.run(sla.NewService(store), tk, at))

			cur := f.currentOf(t, tk.ID)
			require.Equal(t, newID, cur.ID, "the hook ran: the record changed under the service")
			require.False(t, w.at(cur).Valid, "a response from before the reopen is not on the new clock")
			require.False(t, w.breach(cur).Valid)
		})
	}
}

// Every door that records a response or a resolution, with a reopen restart
// committing between the SLA service's read and its write. The doors are the
// call sites in ticket.Service; each must leave the new record untouched.
func TestSLARecordRace_EveryDoorLeavesARestartedRecordAlone(t *testing.T) {
	doors := []struct {
		name string
		run  func(t *testing.T, f *changePriorityFx, svc *ticket.Service)
	}{
		{"AddReply", func(t *testing.T, f *changePriorityFx, svc *ticket.Service) {
			_, err := svc.AddReply(f.ctx, f.tk.ID, "on it", ticket.TextFormatPlain, false, false, "", f.staff, 0, uuid.Nil)
			require.NoError(t, err)
		}},
		{"UpdateStatus to Resolved", func(t *testing.T, f *changePriorityFx, svc *ticket.Service) {
			st, err := f.ts.GetStatusByName(f.ctx, ticket.StatusNameResolved)
			require.NoError(t, err)
			_, err = svc.UpdateStatus(f.ctx, f.tk.ID, st.ID, f.staff)
			require.NoError(t, err)
		}},
		{"Update to Resolved", func(t *testing.T, f *changePriorityFx, svc *ticket.Service) {
			st, err := f.ts.GetStatusByName(f.ctx, ticket.StatusNameResolved)
			require.NoError(t, err)
			_, err = svc.Update(f.ctx, f.tk.ID, ticket.Patch{StatusID: &st.ID}, f.staff)
			require.NoError(t, err)
		}},
		{"Resolve", func(t *testing.T, f *changePriorityFx, svc *ticket.Service) {
			_, err := svc.Resolve(f.ctx, f.tk.ID, "done", f.staff)
			require.NoError(t, err)
		}},
		{"ResolveAsDuplicate", func(t *testing.T, f *changePriorityFx, svc *ticket.Service) {
			other := f.raceTicket(t)
			_, err := svc.ResolveAsDuplicate(f.ctx, f.tk.ID, other.ID, "same", f.staff)
			require.NoError(t, err)
		}},
		{"Close", func(t *testing.T, f *changePriorityFx, svc *ticket.Service) {
			require.NoError(t, svc.Close(f.ctx, f.tk.ID, ticket.Actor{UserID: f.staff.UserID, Role: user.RoleAdmin}))
		}},
	}
	for _, d := range doors {
		t.Run(d.name, func(t *testing.T) {
			f := newChangePriorityFx(t)
			require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
			var newID uuid.UUID
			store := &interposeStore{Store: f.ss, hook: func() {
				restart := time.Now().UTC().Add(time.Second) // after every instant the door can have taken
				newID = f.reMatchNow(t, f.tk.ID, f.pol[ticket.PriorityHigh].ID, &restart)
			}}
			svc := ticket.NewService(f.ts, f.ts, notification.Noop{}, f.au, txrunner.New(f.db.SQL, nil, nil), sla.NewService(store))
			require.NoError(t, svc.LoadSystemStatuses(f.ctx))
			svc.SetSLAEnabled(func(context.Context) bool { return true })

			d.run(t, f, svc)

			cur := f.currentOf(t, f.tk.ID)
			require.Equal(t, newID, cur.ID, "the restart happened between the SLA read and write")
			require.False(t, cur.FirstResponseAt.Valid, "no response from before the restart on the new clock")
			require.False(t, cur.ResolvedAt.Valid, "no resolution from before the restart on the new clock")
			require.False(t, cur.ResponseBreachedAt.Valid)
			require.False(t, cur.ResolutionBreachedAt.Valid)
		})
	}
}

// A re-match that keeps happening is not retried forever: the failure comes
// back as an error, which cmd/server's loggingSLA reports at the boundary.
func TestSLARecordRace_GivesUpAfterRepeatedReMatches(t *testing.T) {
	f := newChangePriorityFx(t)
	lenient, strict := f.racePolicies(t)
	tk := f.tk
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: lenient.ID}))
	flip := []uuid.UUID{strict.ID, lenient.ID}
	var swaps int
	store := &everyReadReMatchedStore{Store: f.ss, onPolicyRead: func() {
		f.reMatchNow(t, tk.ID, flip[swaps%2], nil)
		swaps++
	}}

	err := sla.NewService(store).RecordFirstResponse(f.ctx, tk, time.Now().UTC())

	require.Error(t, err)
	require.True(t, errors.Is(err, sla.ErrRecordSuperseded), "the error says why: %v", err)
	require.Equal(t, 3, swaps, "three attempts, each beaten by a re-match")
}

// everyReadReMatchedStore runs onPolicyRead on every GetPolicy, so every
// attempt the service makes is beaten by a re-match.
type everyReadReMatchedStore struct {
	sla.Store
	onPolicyRead func()
}

func (s *everyReadReMatchedStore) GetPolicy(ctx context.Context, id uuid.UUID) (sla.Policy, error) {
	s.onPolicyRead()
	return s.Store.GetPolicy(ctx, id)
}
