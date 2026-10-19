package txrunner_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// A transaction Postgres aborts to break a lock cycle (40P01) or to refuse a
// serialization conflict (40001) is run again, three attempts in all (#508).

// testLimit bounds every wait in a staged test, so a staging mistake fails
// instead of hanging the suite.
const testLimit = 60 * time.Second

type tx = func(ticket.Store, audit.Store, *notification.Changes) error

type outcome struct {
	attempts int
	err      error
}

// runWithDeadlocks runs InTx on a closure that is killed by a real deadlock on
// its first `kills` attempts. If finalAttempt is true the attempt after the
// last kill is let through to commit.
//
// Staged, not raced. The closure writes audit row X and then audit row Y. A
// partner transaction has already written Y, uncommitted, so the closure's
// insert of Y waits on it (a duplicate key waits for the other transaction to
// finish). Once the closure holds X the partner inserts X: a cycle. The
// partner's deadlock timer is set to an hour (SET LOCAL deadlock_timeout,
// which needs a superuser role: the test role is one) so the closure is
// always the victim.
//
// Each attempt waits at a gate until the test has put a fresh partner in
// place, so a retry cannot start while the previous partner still holds
// anything, whatever the pause between attempts is. Only rows with this
// test's own ids are touched.
func runWithDeadlocks(t *testing.T, kills int, finalAttempt bool) outcome {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	// A cleanup, not a defer: it must run after the partners are rolled back,
	// or closing the pool waits forever on a connection a failed test left
	// blocked.
	t.Cleanup(closeDB)
	ctx := context.Background()

	entityID, idX, idY := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = db.SQL.Exec(`DELETE FROM audit_log WHERE entity_id = $1`, entityID)
	})
	row := func(id uuid.UUID) audit.Entry {
		e := entry(entityID, "retry-row")
		e.ID = id
		return e
	}
	insert := `INSERT INTO audit_log (id, entity_type, entity_id, action) VALUES ($1, 'txrunner-test', $2, 'partner')`

	var attempts int
	gate := make(chan struct{})
	holdsX := make(chan struct{}, 1)
	done := make(chan outcome, 1)

	go func() {
		err := txrunner.New(db.SQL, nil, nil).InTx(ctx, func(_ ticket.Store, au audit.Store, _ *notification.Changes) error {
			attempts++
			select {
			case <-gate:
			case <-time.After(testLimit):
				return errors.New("the test never opened the gate")
			}
			if err := au.Create(ctx, row(idX)); err != nil {
				return err
			}
			holdsX <- struct{}{}
			return au.Create(ctx, row(idY))
		})
		done <- outcome{attempts, err}
	}()

	// open lets the next attempt through, or returns the outcome if InTx has
	// already given up.
	open := func() (outcome, bool) {
		select {
		case gate <- struct{}{}:
			return outcome{}, false
		case o := <-done:
			return o, true
		case <-time.After(testLimit):
			t.Fatal("the closure never reached its gate")
			return outcome{}, true
		}
	}

	for k := 0; k < kills; k++ {
		partner, err := db.SQL.BeginTx(ctx, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = partner.Rollback() })
		_, err = partner.ExecContext(ctx, `SET LOCAL deadlock_timeout = '1h'`)
		require.NoError(t, err, "setting deadlock_timeout needs a superuser role")
		_, err = partner.ExecContext(ctx, insert, idY, entityID)
		require.NoError(t, err)

		if o, finished := open(); finished {
			return o
		}
		select {
		case <-holdsX:
		case <-time.After(testLimit):
			t.Fatal("the closure never wrote its first row")
		}
		// Closes the cycle. Returns when Postgres has killed the closure and
		// the closure's transaction is gone.
		_, err = partner.ExecContext(ctx, insert, idX, entityID)
		require.NoError(t, err, "the partner was killed instead of the closure")
		require.NoError(t, partner.Rollback())
	}
	if finalAttempt {
		if o, finished := open(); finished {
			return o
		}
		// The last attempt has nothing to wait for: Y is free.
		select {
		case <-holdsX:
		case <-time.After(testLimit):
			t.Fatal("the final attempt never wrote its first row")
		}
	}
	select {
	case o := <-done:
		return o
	case <-time.After(testLimit):
		t.Fatal("InTx never returned")
		return outcome{}
	}
}

func requireDeadlock(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "want the deadlock itself, got %v", err)
	require.Equal(t, "40P01", pgErr.Code)
}

func TestInTx_RetriesADeadlockUpToThreeAttemptsThenReturnsIt(t *testing.T) {
	o := runWithDeadlocks(t, 3, false)
	requireDeadlock(t, o.err)
	require.Equal(t, 3, o.attempts)
}

func TestInTx_SucceedsOnTheSecondOrThirdAttempt(t *testing.T) {
	for _, kills := range []int{1, 2} {
		t.Run(fmt.Sprintf("after %d deadlock(s)", kills), func(t *testing.T) {
			o := runWithDeadlocks(t, kills, true)
			require.NoError(t, o.err)
			require.Equal(t, kills+1, o.attempts)
		})
	}
}

// The other retryable code, and the ones that are not, need no real conflict:
// the runner decides on the SQLSTATE alone.
func TestInTx_RetriesOnlyDeadlockAndSerializationFailure(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	tests := []struct {
		name         string
		err          error
		wantAttempts int
	}{
		{"serialization failure", &pgconn.PgError{Code: "40001"}, 3},
		{"deadlock", &pgconn.PgError{Code: "40P01"}, 3},
		{"unique violation", &pgconn.PgError{Code: "23505"}, 1},
		{"not a database error", errors.New("the caller failed"), 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			err := txrunner.New(db.SQL, nil, nil).InTx(context.Background(), func(ticket.Store, audit.Store, *notification.Changes) error {
				attempts++
				return fmt.Errorf("wrapped: %w", tc.err)
			})
			require.ErrorIs(t, err, tc.err)
			require.Equal(t, tc.wantAttempts, attempts)
		})
	}
}

func TestInTx_SerializationFailureThenSuccess(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	attempts := 0
	err := txrunner.New(db.SQL, nil, nil).InTx(context.Background(), func(ticket.Store, audit.Store, *notification.Changes) error {
		attempts++
		if attempts < 3 {
			return &pgconn.PgError{Code: "40001"}
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, attempts)
}

func TestInTx_ACancelledContextStopsTheRetries(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := 0
	err := txrunner.New(db.SQL, nil, nil).InTx(ctx, func(ticket.Store, audit.Store, *notification.Changes) error {
		attempts++
		cancel() // gone after the first kill: no reason to wait for a second go
		return &pgconn.PgError{Code: "40P01"}
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, attempts)
}
