package ratelimit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ratelimitstore"
	"github.com/publiciallc/go-help-desk/backend/internal/ratelimit"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// These run against a real Postgres, in a transaction that is rolled back. The
// counting is one SQL statement, so a fake store would test a different
// implementation from the one that ships.

// newStore is a store inside a rolled-back transaction.
func newStore(t *testing.T) *ratelimitstore.Store {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, rollback := testutil.TxQueries(t, db)
	t.Cleanup(rollback)
	return ratelimitstore.New(q)
}

// newLimiter is a limiter with a clock the test holds still, on a store of its
// own, with a name no other test shares.
func newLimiter(t *testing.T, limit int, window time.Duration) (*ratelimit.Limiter, *time.Time) {
	t.Helper()
	now := time.Unix(1_000_000, 0)
	rl := ratelimit.New(newStore(t), "test-"+uuid.NewString(), limit, window)
	rl.SetClock(func() time.Time { return now })
	return rl, &now
}

func allow(t *testing.T, rl *ratelimit.Limiter, key string) bool {
	t.Helper()
	ok, err := rl.Allow(context.Background(), key)
	require.NoError(t, err)
	return ok
}

func take(t *testing.T, rl *ratelimit.Limiter, key string) ratelimit.Taken {
	t.Helper()
	tk, err := rl.Take(context.Background(), key)
	require.NoError(t, err)
	return tk
}

func TestLimiter(t *testing.T) {
	ctx := context.Background()

	t.Run("allows up to the limit then refuses", func(t *testing.T) {
		rl, _ := newLimiter(t, 3, time.Minute)
		for i := 1; i <= 3; i++ {
			require.True(t, allow(t, rl, "k"), "attempt %d", i)
		}
		require.False(t, allow(t, rl, "k"), "the attempt past the limit must be refused")
	})

	t.Run("counts per key", func(t *testing.T) {
		rl, _ := newLimiter(t, 1, time.Minute)
		require.True(t, allow(t, rl, "a"))
		require.False(t, allow(t, rl, "a"))
		require.True(t, allow(t, rl, "b"), "one key's spending must not charge another")
	})

	// NIST SP 800-63B has the verifier disregard prior failures once the user
	// authenticates successfully; without it, two fumbled codes followed by a
	// correct one still leave the user throttled for the rest of the window.
	t.Run("success clears the budget", func(t *testing.T) {
		rl, _ := newLimiter(t, 2, time.Minute)
		require.True(t, allow(t, rl, "k"))
		require.NoError(t, rl.Reset(ctx, "k"))
		require.True(t, allow(t, rl, "k"))
		require.True(t, allow(t, rl, "k"), "the budget must be the full one again")
	})

	// For a caller that spends before the work and finds the work refused: the
	// attempt did not happen, so it should not have been counted.
	t.Run("a refund gives one attempt back", func(t *testing.T) {
		rl, _ := newLimiter(t, 2, time.Minute)
		require.True(t, allow(t, rl, "k"))
		tk := take(t, rl, "k")
		require.NoError(t, rl.Refund(ctx, "k", tk.Window))
		require.True(t, allow(t, rl, "k"), "the refunded attempt is available again")
		require.False(t, allow(t, rl, "k"))
	})

	// A refund belongs to the window it was spent in. If the window rolled over
	// while the work was in flight, the slot was never taken from the new
	// window, and giving one back would let a key exceed its limit there.
	t.Run("a refund is ignored once the window it was spent in has ended", func(t *testing.T) {
		rl, now := newLimiter(t, 1, time.Minute)
		first := take(t, rl, "k")
		require.True(t, first.Allowed)
		*now = now.Add(2 * time.Minute)
		second := take(t, rl, "k") // opens a new window and spends its one slot
		require.True(t, second.Allowed)
		require.NoError(t, rl.Refund(ctx, "k", first.Window))
		require.False(t, allow(t, rl, "k"), "the new window's slot was not refunded")
	})

	t.Run("a refund never goes below zero or touches another key", func(t *testing.T) {
		rl, _ := newLimiter(t, 1, time.Minute)
		tk := take(t, rl, "k")
		require.NoError(t, rl.Refund(ctx, "k", tk.Window)) // takes the one spent slot back
		require.NoError(t, rl.Refund(ctx, "k", tk.Window)) // nothing left to give back
		require.True(t, allow(t, rl, "k"))
		require.False(t, allow(t, rl, "k"), "a refund with nothing spent must not add budget")
		require.NoError(t, rl.Refund(ctx, "other", tk.Window))
		require.False(t, allow(t, rl, "k"))
	})

	t.Run("the window resets", func(t *testing.T) {
		rl, now := newLimiter(t, 1, time.Minute)
		require.True(t, allow(t, rl, "k"))
		require.False(t, allow(t, rl, "k"))
		*now = now.Add(time.Minute + time.Second)
		require.True(t, allow(t, rl, "k"), "the window must expire and allow again")
	})

	t.Run("a limit of zero disables it", func(t *testing.T) {
		rl, _ := newLimiter(t, 0, time.Minute)
		for i := 0; i < 50; i++ {
			require.True(t, allow(t, rl, "k"))
		}
		require.NoError(t, rl.Reset(ctx, "k"))
		exceeded, err := rl.Exceeded(ctx, "k")
		require.NoError(t, err)
		require.False(t, exceeded)
	})
}

// TakeWithLimit takes its budget from the caller, so a limit an administrator
// edits applies on the next call; SetClock lets the window pass without
// sleeping (#445).
func TestLimiter_TakeWithLimitAndInjectedClock(t *testing.T) {
	ctx := context.Background()
	rl, now := newLimiter(t, 1, time.Hour)

	for i := range 2 {
		tk, err := rl.TakeWithLimit(ctx, "a", 2)
		require.NoError(t, err)
		require.True(t, tk.Allowed, "take %d of 2", i+1)
	}
	tk, err := rl.TakeWithLimit(ctx, "a", 2)
	require.NoError(t, err)
	require.False(t, tk.Allowed, "third is over a limit of 2")
	require.Equal(t, time.Hour, tk.RetryAfter)
	tk, err = rl.TakeWithLimit(ctx, "b", 2)
	require.NoError(t, err)
	require.True(t, tk.Allowed, "another key has its own count")

	// Raising the limit applies at once: the same count is now within it.
	tk, err = rl.TakeWithLimit(ctx, "a", 10)
	require.NoError(t, err)
	require.True(t, tk.Allowed)

	*now = now.Add(61 * time.Minute)
	tk, err = rl.TakeWithLimit(ctx, "a", 2)
	require.NoError(t, err)
	require.True(t, tk.Allowed, "a new window")
	tk, err = rl.TakeWithLimit(ctx, "a", 0)
	require.NoError(t, err)
	require.True(t, tk.Allowed, "zero means no limit")
}

// RetryAfter is what a 429's Retry-After is built from: the time until the
// window that refused the caller ends, not the length of a whole window (#421).
func TestLimiter_RetryAfterIsTheTimeLeftInTheKeysWindow(t *testing.T) {
	rl, now := newLimiter(t, 1, 2*time.Second)
	require.Equal(t, 2*time.Second, take(t, rl, "k").RetryAfter)

	*now = now.Add(150 * time.Millisecond)
	require.Equal(t, 1850*time.Millisecond, take(t, rl, "k").RetryAfter,
		"the time remaining must fall as the window runs out")

	// A limiter with nothing to wait for reports nothing.
	off, _ := newLimiter(t, 0, time.Minute)
	require.Zero(t, take(t, off, "k").RetryAfter)
}

// Each key's window starts at its own first attempt. With one window shared by
// every key (the in-memory limiter's), a key first used at 59 minutes was reset
// a minute later, and Retry-After promised a wait that depended on other
// people's traffic.
func TestLimiter_EachKeyHasItsOwnWindow(t *testing.T) {
	rl, now := newLimiter(t, 1, time.Hour)

	require.True(t, take(t, rl, "early").Allowed)
	*now = now.Add(30 * time.Minute)
	late := take(t, rl, "late")
	require.True(t, late.Allowed)

	*now = now.Add(31 * time.Minute) // 61 minutes in: early's window is over, late's is not
	require.True(t, take(t, rl, "early").Allowed, "early's own window ended")
	refused := take(t, rl, "late")
	require.False(t, refused.Allowed, "late's window has 29 minutes left, whatever early did")
	require.Equal(t, 29*time.Minute, refused.RetryAfter)
}

// The count is in the database, so a second limiter on the same store (a
// restarted server, or another replica) sees what the first one spent. This is
// the point of #621.
func TestLimiter_CountsAreSharedThroughTheStore(t *testing.T) {
	store := newStore(t)
	now := time.Unix(1_000_000, 0)
	a := ratelimit.New(store, "shared", 2, time.Hour)
	b := ratelimit.New(store, "shared", 2, time.Hour)
	a.SetClock(func() time.Time { return now })
	b.SetClock(func() time.Time { return now })

	require.True(t, allow(t, a, "k"))
	require.True(t, allow(t, b, "k"))
	require.False(t, allow(t, a, "k"), "the budget was spent across two processes")
	require.False(t, allow(t, b, "k"))

	exceeded, err := b.Exceeded(context.Background(), "k")
	require.NoError(t, err)
	require.True(t, exceeded)
}

// Two limiters that count different things must not see each other's keys.
func TestLimiter_NamesKeepLimitersApart(t *testing.T) {
	store := newStore(t)
	a := ratelimit.New(store, "one", 1, time.Hour)
	b := ratelimit.New(store, "two", 1, time.Hour)
	require.True(t, allow(t, a, "k"))
	require.True(t, allow(t, b, "k"), "the same key under another limiter is a different budget")
}

// Exceeded answers the question without spending an attempt, which is what
// lets the handler ask it before doing the expensive part.
func TestExceeded_DoesNotCount(t *testing.T) {
	ctx := context.Background()
	rl, now := newLimiter(t, 2, time.Minute)
	exceeded := func() bool {
		t.Helper()
		ex, err := rl.Exceeded(ctx, "k")
		require.NoError(t, err)
		return ex
	}

	require.False(t, exceeded())
	for range 20 {
		require.False(t, exceeded(), "asking the question spent an attempt")
	}

	require.True(t, allow(t, rl, "k"))
	require.False(t, exceeded())
	require.True(t, allow(t, rl, "k"))
	require.True(t, exceeded(), "the budget is spent and Exceeded says otherwise")

	*now = now.Add(2 * time.Minute)
	require.False(t, exceeded(), "a window that has ended spends nothing")

	require.True(t, allow(t, rl, "k"))
	require.True(t, allow(t, rl, "k"))
	require.NoError(t, rl.Reset(ctx, "k"))
	require.False(t, exceeded(), "a correct password should clear the budget")
}

// A database that cannot be asked must not be read as "nothing spent": the
// limits guard mail to strangers and guesses at passwords, so they fail closed.
func TestLimiter_StoreErrorIsAnErrorAndNotAnAllow(t *testing.T) {
	boom := errors.New("db down")
	rl := ratelimit.New(failingStore{boom}, "x", 5, time.Minute)

	tk, err := rl.Take(context.Background(), "k")
	require.ErrorIs(t, err, boom)
	require.False(t, tk.Allowed)

	ok, err := rl.Allow(context.Background(), "k")
	require.ErrorIs(t, err, boom)
	require.False(t, ok)

	_, err = rl.Exceeded(context.Background(), "k")
	require.ErrorIs(t, err, boom)
}

type failingStore struct{ err error }

func (f failingStore) Take(context.Context, string, time.Duration, time.Time) (ratelimit.Count, error) {
	return ratelimit.Count{}, f.err
}
func (f failingStore) Refund(context.Context, string, time.Time) error { return f.err }
func (f failingStore) Spent(context.Context, string, time.Time) (int, error) {
	return 0, f.err
}
func (f failingStore) Reset(context.Context, string) error { return f.err }
