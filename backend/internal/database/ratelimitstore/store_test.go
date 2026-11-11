package ratelimitstore_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ratelimitstore"
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

func countRows(t *testing.T, db *testutil.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM rate_limit_counters`).Scan(&n))
	return n
}
