package ratelimitstore_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ratelimitstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/ratelimit"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Both tests here commit, and the first needs several connections at once, so
// they run in a database of their own (testutil.NewIsolatedDB) rather than in
// the rolled-back transaction every other test shares.

// The reason the counter is one statement: two attempts at the limit at the
// same moment must not both be told they were inside it. Many goroutines, each
// through its own limiter and store as separate replicas would be, all aimed at
// one key, with a limit of five: exactly five get through, however the
// statements interleave. A read-then-write counter lets more than five by.
func TestConcurrentAttemptsCannotBothPassTheLimit(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()

	const (
		replicas = 4
		perProc  = 25
		limit    = 5
	)
	now := time.Unix(2_000_000, 0)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	start := make(chan struct{})
	for range replicas {
		// A new store and limiter per "replica": nothing but the database is
		// shared between them.
		rl := ratelimit.New(ratelimitstore.New(db.Queries), "race", limit, time.Hour)
		rl.SetClock(func() time.Time { return now })
		for range perProc {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ok, err := rl.Allow(context.Background(), "one-key")
				if err != nil {
					t.Errorf("take: %v", err)
					return
				}
				if ok {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}()
		}
	}
	close(start)
	wg.Wait()

	require.Equal(t, limit, allowed,
		"%d attempts at a limit of %d let %d through", replicas*perProc, limit, allowed)
}

// A window that ends is replaced by the next attempt, in place, so a key that
// keeps being used is one row for ever and not one row per window.
func TestAnExpiredWindowIsReplacedNotAdded(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	s := ratelimitstore.New(db.Queries)
	t0 := time.Unix(3_000_000, 0)

	for i := range 5 {
		c, err := s.Take(ctx, "k", time.Minute, t0.Add(time.Duration(i)*2*time.Minute))
		require.NoError(t, err)
		require.Equal(t, 1, c.N, "each attempt opens a fresh window")
	}
	require.Equal(t, 1, countRows(t, db))
}

// The table must not grow without bound: keys are partly chosen by the caller
// (an email address typed into the sign-in form), so rows whose window has ended
// are deleted. Live ones stay. More expired rows than one batch, so the loop is
// exercised and not just the first statement.
func TestSweepDeletesExpiredRowsAndOnlyThose(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	s := ratelimitstore.New(db.Queries)
	now := time.Unix(4_000_000, 0)

	expired := ratelimitstore.SweepBatch*2 + 7
	_, err := db.SQL.ExecContext(ctx, `
		INSERT INTO rate_limit_counters (key, window_start, window_end, count)
		SELECT 'old-' || g, $1::timestamptz - interval '2 hours', $1::timestamptz - interval '1 hour', 1
		FROM generate_series(1, $2) g`, now, expired)
	require.NoError(t, err)

	// One live row, ending an hour from now.
	_, err = s.Take(ctx, "live", time.Hour, now)
	require.NoError(t, err)
	require.Equal(t, expired+1, countRows(t, db))

	n, err := s.DeleteExpired(ctx, now)
	require.NoError(t, err)
	require.EqualValues(t, expired, n)
	require.Equal(t, 1, countRows(t, db), "only the live row is left")

	spent, err := s.Spent(ctx, "live", now)
	require.NoError(t, err)
	require.Equal(t, 1, spent, "a live budget is not swept")
}

// The sweep picks expired keys and then deletes them. A request that reopens
// one of those keys between the two must keep its new, live window: deleting by
// key alone removed it, and the key's budget started again from nothing.
//
// 20,000 expired keys, the sweep running against eight goroutines that each
// reopen a share of them. Every key a goroutine reopened is live afterwards, so
// the live rows must be exactly the keys that were reopened.
func TestSweepDoesNotDeleteAWindowReopenedDuringIt(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	s := ratelimitstore.New(db.Queries)
	now := time.Now()

	const keys, workers = 20000, 8
	_, err := db.SQL.ExecContext(ctx, `
		INSERT INTO rate_limit_counters (key, window_start, window_end, count)
		SELECT 'old-' || g, $1::timestamptz - interval '2 hours', $1::timestamptz - interval '1 hour', 1
		FROM generate_series(1, $2) g`, now, keys)
	require.NoError(t, err)

	var wg sync.WaitGroup
	start := make(chan struct{})
	reopened := make([]int, workers)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := w; i < keys; i += workers {
				c, err := s.Take(ctx, "old-"+strconv.Itoa(i+1), time.Hour, now)
				if err != nil {
					t.Errorf("take: %v", err)
					return
				}
				if c.N != 1 {
					t.Errorf("key old-%d was not reopened: count %d", i+1, c.N)
				}
				reopened[w]++
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if _, err := s.DeleteExpired(ctx, now); err != nil {
			t.Errorf("sweep: %v", err)
		}
	}()
	close(start)
	wg.Wait()

	total := 0
	for _, n := range reopened {
		total += n
	}
	var live int
	require.NoError(t, db.SQL.QueryRowContext(ctx,
		`SELECT count(*) FROM rate_limit_counters WHERE window_end > $1`, now).Scan(&live))
	require.Equal(t, total, live, "a live window was deleted by the sweep that raced it")
}

// The same race, made certain instead of likely. Connection A reopens an
// expired key in a transaction and holds it open, so the row is locked and the
// committed version is still the expired one. The sweep (connection B) picks the
// key from that old version and its DELETE blocks on the lock. Only when A
// commits does the DELETE go on, and it must re-test the row it now finds: a
// reopened window no longer matches. Without the expiry test in the outer WHERE
// the DELETE goes by key alone and removes the live window, every time.
func TestSweepWaitsForAReopenAndThenKeepsIt(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	now := time.Now()

	_, err := db.SQL.ExecContext(ctx, `
		INSERT INTO rate_limit_counters (key, window_start, window_end, count)
		VALUES ('k', $1::timestamptz - interval '2 hours', $1::timestamptz - interval '1 hour', 1)`, now)
	require.NoError(t, err)

	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	c, err := ratelimitstore.New(dbgen.New(tx)).Take(ctx, "k", time.Hour, now)
	require.NoError(t, err)
	require.Equal(t, 1, c.N, "reopened, and the row is locked until the commit")

	swept := make(chan error, 1)
	go func() {
		_, err := ratelimitstore.New(db.Queries).DeleteExpired(ctx, now)
		swept <- err
	}()

	// Wait until the sweep's DELETE is blocked on the lock, so the commit below
	// is certain to come after it chose the key.
	require.Eventually(t, func() bool {
		var n int
		require.NoError(t, db.SQL.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND pid <> pg_backend_pid()
			  AND query ILIKE '%DELETE FROM rate_limit_counters%'`).Scan(&n))
		return n > 0
	}, 10*time.Second, 20*time.Millisecond, "the sweep never reached the row lock")

	require.NoError(t, tx.Commit())
	require.NoError(t, <-swept)

	var live int
	require.NoError(t, db.SQL.QueryRowContext(ctx,
		`SELECT count(*) FROM rate_limit_counters WHERE key = 'k' AND window_end > $1`, now).Scan(&live))
	require.Equal(t, 1, live, "the sweep deleted a window that was reopened while it ran")
}

func countRows(t *testing.T, db *testutil.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM rate_limit_counters`).Scan(&n))
	return n
}

// One sweep batch removes at most batch rows, whatever plan Postgres picks. The
// statement used to be `key IN (SELECT ... LIMIT n)`, and under a nested-loop
// semi join the subquery was re-run per row of the table, so a batch of 3 could
// remove every expired row. The transaction and the forced plan keep this
// deterministic; see testutil.ForceRescannedBatchPlan.
func TestDeleteExpiredRateLimits_NeverDeletesMoreThanItsBatch(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	testutil.ForceRescannedBatchPlan(t, tx)

	// Windows ended in 1970, so a cutoff in February 1970 matches these and
	// nothing else a long-lived test database might hold.
	for i := range 7 {
		_, err := tx.Exec(`INSERT INTO rate_limit_counters (key, window_start, window_end, count)
			VALUES ($1, '1970-01-01', '1970-01-02'::timestamptz + $2 * interval '1 second', 1)`,
			"batch-test-"+strconv.Itoa(i), i)
		require.NoError(t, err)
	}
	n, err := q.DeleteExpiredRateLimits(context.Background(), dbgen.DeleteExpiredRateLimitsParams{
		Now: time.Date(1970, 2, 1, 0, 0, 0, 0, time.UTC), Batch: 3,
	})
	require.NoError(t, err)
	require.EqualValues(t, 3, n)

	var left int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM rate_limit_counters WHERE key LIKE 'batch-test-%'`).Scan(&left))
	require.Equal(t, 4, left)
}
