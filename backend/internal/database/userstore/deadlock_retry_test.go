package userstore_test

import (
	"context"
	"database/sql"
	"fmt"
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
// but staged by ordering instead of by a slow wake-up, which cannot be
// provoked without freezing a Postgres backend.
//
// Postgres aborts whichever backend's deadlock_timeout fires first, which can
// be a partner rather than the guarded statement, and the test would then
// pass with fewer kills than it claims. So the partners' deadlock detector is
// switched off (SET LOCAL deadlock_timeout = '1h', which needs a superuser
// role: the test role is one, and the test fails loudly if it is not), and the
// guarded statement is always the victim. Each partner's own statement error
// is required to be nil, and the number of retries is counted and required to
// be exactly two, so the test cannot pass without both kills.
//
// The pause before each retry is replaced (Store.SetRetryPause) with a wait
// for the partner that closed the cycle to finish and roll back, so a retry
// cannot start while that partner is still queued for the administrator row
// and take it first, which is how #508 failed. The old single retry fails
// here with 40P01; three attempts pass.
func TestGuardedWrite_SurvivesTwoDeadlocks(t *testing.T) {
	db, closeDB := freshDatabase(t)
	// Cleanup, not defer: the held transactions below are rolled back by
	// cleanups registered after this one, and closing the database waits for the
	// guarded statement, which waits for them. A deferred close would run first
	// and, on any failure, hang there instead of reporting it.
	t.Cleanup(closeDB)
	store := userstore.New(dbgen.New(db.SQL))
	ctx := context.Background()

	adminA := seedAdmin(t, store)
	seedAdmin(t, store)
	victim := seedStaff(t, store)

	holdVictim := func() *sql.Tx {
		tx, err := db.SQL.BeginTx(ctx, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = tx.Rollback() })
		_, err = tx.ExecContext(ctx, `SET LOCAL deadlock_timeout = '1h'`)
		require.NoError(t, err, "setting deadlock_timeout needs a superuser role")
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
	// Attempt N+1 waits for partner N to be completely gone.
	partnerDone := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var partnerErr [2]error
	closeCycle := func(i int, tx *sql.Tx) {
		go func() {
			defer close(partnerDone[i])
			_, partnerErr[i] = tx.ExecContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, adminA)
			_ = tx.Rollback()
		}()
	}

	// A bounded wait for a partner. Closing a cycle can take longer than a
	// second on a loaded runner, and an unbounded receive would then hold the
	// run until the go test timeout instead of failing here (#523).
	const partnerWait = 60 * time.Second
	awaitPartner := func(ctx context.Context, i int) error {
		select {
		case <-partnerDone[i]:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(partnerWait):
			return fmt.Errorf("partner %d never finished within %s", i, partnerWait)
		}
	}

	pauses := 0
	store.SetRetryPause(func(ctx context.Context) error {
		i := pauses
		pauses++
		return awaitPartner(ctx, i)
	})

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
	closeCycle(0, first)
	// Not before the first partner is gone: until then the waiter seen below
	// could still be the attempt that is about to be killed.
	require.NoError(t, awaitPartner(ctx, 0))

	require.Eventually(t, guardedWaiting, 30*time.Second, 5*time.Millisecond, "the retry never reached the held row")
	closeCycle(1, second)

	select {
	case a := <-disabled:
		require.NoError(t, a.err, "the disable was killed twice and reported as a failure")
		require.True(t, a.applied, "the disable should have applied on the third attempt")
	case <-time.After(60 * time.Second):
		t.Fatal("the disable never finished")
	}
	require.NoError(t, awaitPartner(ctx, 1))

	// A partner that was killed means the guarded statement was not the
	// victim, and the kill this test is about did not happen.
	require.NoError(t, partnerErr[0], "the first partner was killed instead of the disable")
	require.NoError(t, partnerErr[1], "the second partner was killed instead of the retry")
	require.Equal(t, 2, pauses, "the disable should have been killed twice, and so retried twice")

	var off bool
	require.NoError(t, db.SQL.QueryRow(`SELECT disabled FROM users WHERE id = $1`, victim).Scan(&off))
	require.True(t, off, "the account is still enabled, so the disable was lost")
}
