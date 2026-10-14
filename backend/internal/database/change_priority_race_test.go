package database_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// ChangePriority racing the other writes to one ticket, against real Postgres
// (#443b). Each race runs many times on fresh tickets, because a race that
// happens to go one way once proves nothing about the other order. The
// properties: nothing deadlocks, a Resolved or Closed ticket's priority never
// changes, a ticket never has two current SLA records, and the current record
// is always under the final priority's policy.

const raceRounds = 15

// raceTicket plants another medium ticket in the fixture's category, with a
// current record under the medium policy started at its creation.
func (f *changePriorityFx) raceTicket(t *testing.T) ticket.Ticket {
	t.Helper()
	tk := f.tk
	tk.ID = uuid.New()
	tk.TrackingNumber = ticket.TrackingNumber("CPR-" + uuid.NewString()[:8])
	require.NoError(t, f.ts.Create(f.ctx, tk))
	t.Cleanup(func() {
		_, _ = f.db.SQL.Exec(`DELETE FROM tickets WHERE id = $1`, tk.ID)
		_, _ = f.db.SQL.Exec(`DELETE FROM audit_log WHERE entity_id = $1`, tk.ID)
	})
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
	return tk
}

// raceService is the fixture's service with the real SLA service wired, so the
// racing writes also run RecordFirstResponse and RecordResolved after their
// commit, outside the ticket lock, the way production does.
func (f *changePriorityFx) raceService(t *testing.T) *ticket.Service {
	t.Helper()
	svc := ticket.NewService(f.ts, f.ts, notification.Noop{}, f.au, txrunner.New(f.db.SQL), sla.NewService(f.ss))
	require.NoError(t, svc.LoadSystemStatuses(f.ctx))
	svc.SetSLAEnabled(func(context.Context) bool { return true })
	return svc
}

func (f *changePriorityFx) recordsOf(t *testing.T, id uuid.UUID) (all []dbgen.SlaRecord, current []dbgen.SlaRecord) {
	t.Helper()
	all, err := f.q.ListSLARecordsForTicket(f.ctx, id)
	require.NoError(t, err)
	for _, r := range all {
		if !r.SupersededAt.Valid {
			current = append(current, r)
		}
	}
	return all, current
}

func (f *changePriorityFx) actionsOf(t *testing.T, id uuid.UUID) map[string]int {
	t.Helper()
	entries, err := f.au.ListByEntity(f.ctx, "ticket", id, 100, 0)
	require.NoError(t, err)
	out := map[string]int{}
	for _, e := range entries {
		out[e.Action]++
	}
	return out
}

// race runs a and b at once and waits for both, failing if either takes longer
// than a deadlock timeout would.
func race(t *testing.T, a, b func()) {
	t.Helper()
	done := make(chan struct{})
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, fn := range []func(){a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; fn() }()
	}
	go func() { wg.Wait(); close(done) }()
	close(start)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the two writes did not finish: deadlocked or stuck on a lock")
	}
}

func TestChangePriority_RacingResolve(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.raceService(t)
	var changedFirst, refused int
	for i := 0; i < raceRounds; i++ {
		tk := f.raceTicket(t)
		var cpErr, resErr error
		race(t,
			func() { _, cpErr = svc.ChangePriority(f.ctx, tk.ID, ticket.PriorityHigh, f.staff) },
			func() { _, resErr = svc.Resolve(f.ctx, tk.ID, "done", f.staff) },
		)
		require.NoError(t, resErr)
		stored, err := f.ts.GetByID(f.ctx, tk.ID)
		require.NoError(t, err)
		all, current := f.recordsOf(t, tk.ID)
		require.Len(t, current, 1, "exactly one current record")
		require.True(t, current[0].ResolvedAt.Valid, "the resolution lands on the current record")
		switch {
		case cpErr == nil:
			changedFirst++
			require.Equal(t, ticket.PriorityHigh, stored.Priority)
			require.Len(t, all, 2)
			require.Equal(t, f.pol[ticket.PriorityHigh].ID, current[0].PolicyID)
			require.Equal(t, 1, f.actionsOf(t, tk.ID)["priority_changed"])
		case errors.Is(cpErr, ticket.ErrClosed):
			refused++
			require.Equal(t, ticket.PriorityMedium, stored.Priority, "a Resolved ticket's priority never changes")
			require.Len(t, all, 1)
			require.Zero(t, f.actionsOf(t, tk.ID)["priority_changed"])
		default:
			t.Fatalf("round %d: ChangePriority: %v", i, cpErr)
		}
	}
	t.Logf("priority first: %d, refused as resolved: %d", changedFirst, refused)
}

func TestChangePriority_RacingClose(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.raceService(t)
	admin := ticket.Actor{UserID: f.staff.UserID, Role: user.RoleAdmin}
	for i := 0; i < raceRounds; i++ {
		tk := f.raceTicket(t)
		var cpErr, closeErr error
		race(t,
			func() { _, cpErr = svc.ChangePriority(f.ctx, tk.ID, ticket.PriorityCritical, f.staff) },
			func() { closeErr = svc.Close(f.ctx, tk.ID, admin) },
		)
		require.NoError(t, closeErr)
		stored, err := f.ts.GetByID(f.ctx, tk.ID)
		require.NoError(t, err)
		_, current := f.recordsOf(t, tk.ID)
		require.Len(t, current, 1)
		if cpErr != nil {
			require.ErrorIs(t, cpErr, ticket.ErrClosed, "round %d", i)
			require.Equal(t, ticket.PriorityMedium, stored.Priority)
			continue
		}
		require.Equal(t, ticket.PriorityCritical, stored.Priority)
		require.Equal(t, f.pol[ticket.PriorityCritical].ID, current[0].PolicyID)
	}
}

// A staff reply racing a priority change: both land, and the reply's first
// response is recorded on a current record (unless the window the DESIGN.md
// known limit describes is hit, which is counted and logged, not failed).
func TestChangePriority_RacingStaffReply(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.raceService(t)
	var missed int
	for i := 0; i < raceRounds; i++ {
		tk := f.raceTicket(t)
		var cpErr, replyErr error
		race(t,
			func() { _, cpErr = svc.ChangePriority(f.ctx, tk.ID, ticket.PriorityHigh, f.staff) },
			func() {
				_, replyErr = svc.AddReply(f.ctx, tk.ID, "on it", false, false, "", f.staff, 0, uuid.Nil)
			},
		)
		require.NoError(t, cpErr, "round %d", i)
		require.NoError(t, replyErr, "round %d", i)
		_, current := f.recordsOf(t, tk.ID)
		require.Len(t, current, 1)
		require.Equal(t, f.pol[ticket.PriorityHigh].ID, current[0].PolicyID)
		require.False(t, current[0].StartedAt.Valid, "a staff change never moves the clock")
		if !current[0].FirstResponseAt.Valid {
			missed++
		}
	}
	t.Logf("first responses lost to the known-limit window: %d of %d", missed, raceRounds)
}

// Three staff changes at once, many times: they serialise on the row lock and
// leave one current record under the final priority's policy.
func TestChangePriority_ThreeAtOnce(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.raceService(t)
	for i := 0; i < raceRounds; i++ {
		tk := f.raceTicket(t)
		var wg sync.WaitGroup
		errs := make([]error, 3)
		for j, p := range []ticket.Priority{ticket.PriorityHigh, ticket.PriorityCritical, ticket.PriorityLow} {
			wg.Add(1)
			go func() { defer wg.Done(); _, errs[j] = svc.ChangePriority(f.ctx, tk.ID, p, f.staff) }()
		}
		wg.Wait()
		for _, err := range errs {
			require.NoError(t, err)
		}
		stored, err := f.ts.GetByID(f.ctx, tk.ID)
		require.NoError(t, err)
		_, current := f.recordsOf(t, tk.ID)
		if stored.Priority == ticket.PriorityLow {
			require.Empty(t, current, "no policy matches low: the record is superseded and none is current")
		} else {
			require.Len(t, current, 1)
			require.Equal(t, f.pol[stored.Priority].ID, current[0].PolicyID)
		}
	}
}

// The change's timestamps are taken once it holds the row lock. A change that
// waited behind another write must not stamp the record it supersedes with a
// time from before it got the lock: superseded_at orders a ticket's history
// (ListSLARecordsForTicket breaks a tie between records sharing a clock on
// it), and a stale one puts a record before the record it replaced.
func TestChangePriority_TimestampsAreTakenUnderTheLock(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))

	tx, err := f.db.SQL.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(f.ctx, `SELECT id FROM tickets WHERE id = $1 FOR UPDATE`, f.tk.ID)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityHigh, f.staff)
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	released := time.Now()
	require.NoError(t, tx.Commit())
	require.NoError(t, <-done)

	all, _ := f.recordsOf(t, f.tk.ID)
	require.Len(t, all, 2)
	for _, r := range all {
		if r.SupersededAt.Valid {
			require.False(t, r.SupersededAt.Time.Before(released.Add(-time.Millisecond)),
				"superseded at %s, but the lock was only free at %s", r.SupersededAt.Time, released)
		}
	}
	stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.False(t, stored.UpdatedAt.Before(released.Add(-time.Millisecond)), "updated_at is from before the lock")
}
