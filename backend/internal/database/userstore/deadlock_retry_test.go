package userstore_test

import (
	"context"
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
