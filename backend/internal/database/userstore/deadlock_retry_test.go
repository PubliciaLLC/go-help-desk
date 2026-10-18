package userstore_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// An administrator's disable must not fail because somebody else was
// assigning a ticket at the same moment.
//
// The two paths take the same two row locks in opposite orders. Assigning
// reads the assignee with FOR SHARE and then writes an audit row; disabling
// locks every administrator row and then writes the target. When they cross,
// Postgres kills one of them to break the cycle — and it kills the one that
// has waited longest, which is the disable, because it starts by locking the
// whole administrator set and sits there. The assignment commits, and the
// administrator is told "an internal error occurred" for a change that did
// nothing.
//
// The ticket side already retried once on a deadlock; this side did not.
//
// Staged rather than raced: one transaction holds the target row, the guarded
// statement walks into it, and then that transaction reaches for an
// administrator row to close the cycle. A race that reproduces one time in
// ten is a test that passes nine times against the broken code.
func TestGuardedWrite_SurvivesADeadlock(t *testing.T) {
	db, closeDB := freshDatabase(t)
	defer closeDB()
	store := userstore.New(dbgen.New(db.SQL))
	ctx := context.Background()

	// Two administrators, so the last-admin guard itself allows the write —
	// what is being tested is the deadlock, not the guard.
	adminA := seedAdmin(t, store)
	seedAdmin(t, store)
	victim := seedStaff(t, store)

	// The other side of the cycle: a transaction holding the target row the
	// way an assignment holds its assignee.
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.ExecContext(ctx, `SELECT id FROM users WHERE id = $1 FOR SHARE`, victim)
	require.NoError(t, err)

	type answer struct {
		applied bool
		err     error
	}
	disabled := make(chan answer, 1)
	go func() {
		applied, err := store.DisableUnlessLastAdmin(ctx, victim)
		disabled <- answer{applied, err}
	}()

	// Give the disable time to lock the administrators and arrive at the
	// target row, where it now waits on the transaction above.
	select {
	case a := <-disabled:
		t.Fatalf("the disable answered (applied=%v, err=%v) without ever waiting on the "+
			"held row, so this test is no longer staging what it claims", a.applied, a.err)
	case <-time.After(500 * time.Millisecond):
	}

	// Close the cycle. This blocks until Postgres kills one of the two, then
	// this transaction gets the lock and lets go of everything.
	closed := make(chan error, 1)
	go func() {
		_, err := tx.ExecContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, adminA)
		_ = tx.Rollback()
		closed <- err
	}()

	select {
	case a := <-disabled:
		require.NoError(t, a.err,
			"the administrator's disable was killed to break a lock cycle and reported as a failure")
		require.True(t, a.applied, "the disable should have applied on the second attempt")
	case <-time.After(30 * time.Second):
		t.Fatal("the disable never finished")
	}

	<-closed

	var off bool
	require.NoError(t, db.SQL.QueryRow(`SELECT disabled FROM users WHERE id = $1`, victim).Scan(&off))
	require.True(t, off, "the account is still enabled, so the disable was lost")
}

func seedStaff(t *testing.T, store *userstore.Store) uuid.UUID {
	t.Helper()
	u := user.User{
		ID:          uuid.New(),
		Email:       "deadlock-staff-" + uuid.NewString() + "@test.local",
		DisplayName: "Deadlock Staff",
		Role:        user.RoleStaff,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, store.Create(context.Background(), u))
	return u.ID
}

// A second deadlock, on the retry itself.
//
// One transaction was not the only thing that could be late. Two transactions
// hold the target row; the first closes a cycle with the disable and kills it,
// and the disable's retry then walks into the second one, which closes
// another. Each of those is the shape that failed in CI (#508) — a retry
// killed because another transaction was still queued for a row it wanted —
// but staged here by ordering instead of by a slow wake-up, which cannot be
// provoked without freezing a Postgres backend.
//
// Every step waits for the state the next one needs (a backend seen waiting
// on a lock) rather than for a delay, so it does not depend on timing. The
// first cycle is closed while the disable is the oldest waiter, the second
// while the retry is, and Postgres kills the oldest each time. The old single
// retry fails here with 40P01; three attempts pass.
func TestGuardedWrite_SurvivesTwoDeadlocks(t *testing.T) {
	db, closeDB := freshDatabase(t)
	defer closeDB()
	store := userstore.New(dbgen.New(db.SQL))
	ctx := context.Background()

	adminA := seedAdmin(t, store)
	seedAdmin(t, store)
	victim := seedStaff(t, store)

	holdVictim := func() *sql.Tx {
		tx, err := db.SQL.BeginTx(ctx, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = tx.Rollback() })
		_, err = tx.ExecContext(ctx, `SELECT id FROM users WHERE id = $1 FOR SHARE`, victim)
		require.NoError(t, err)
		return tx
	}
	first, second := holdVictim(), holdVictim()

	// The guarded statements are the only ones that mention the CTE.
	guardedWaiting := func() bool {
		var n int
		require.NoError(t, db.SQL.QueryRow(`
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%admins%'`).Scan(&n))
		return n == 1
	}
	// closeCycle reaches for an administrator row from a partner transaction
	// and lets go of everything once it has it.
	closeCycle := func(tx *sql.Tx) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = tx.ExecContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, adminA)
			_ = tx.Rollback()
		}()
		return done
	}

	type answer struct {
		applied bool
		err     error
	}
	disabled := make(chan answer, 1)
	go func() {
		applied, err := store.DisableUnlessLastAdmin(ctx, victim)
		disabled <- answer{applied, err}
	}()

	require.Eventually(t, guardedWaiting, 30*time.Second, 5*time.Millisecond, "the disable never reached the held row")
	firstDone := closeCycle(first)
	<-firstDone // the disable was killed once and the first partner has finished

	require.Eventually(t, guardedWaiting, 30*time.Second, 5*time.Millisecond, "the retry never reached the held row")
	secondDone := closeCycle(second)

	select {
	case a := <-disabled:
		require.NoError(t, a.err, "the disable was killed twice and reported as a failure")
		require.True(t, a.applied, "the disable should have applied on the third attempt")
	case <-time.After(60 * time.Second):
		t.Fatal("the disable never finished")
	}
	<-secondDone

	var off bool
	require.NoError(t, db.SQL.QueryRow(`SELECT disabled FROM users WHERE id = $1`, victim).Scan(&off))
	require.True(t, off, "the account is still enabled, so the disable was lost")
}
