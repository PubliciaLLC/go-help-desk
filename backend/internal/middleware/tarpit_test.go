package middleware_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// The tarpit slows an over-budget key down, one turn at a time, and never
// makes anybody wait longer than twice the delay.
//
// Both halves matter and they pull against each other. Without the turn-taking
// the delay bounds nothing: fifty requests that each sleep a second in
// parallel still make fifty guesses in a second. Without the bound on waiting,
// the queue itself becomes the weapon — the first version of this refused
// anything past an eight-deep queue, and an attacker with eight connections
// and a known email address got the account's owner refused on their own
// correct password. That is the lockout the login handler's ordering exists to
// prevent, reintroduced by the thing meant to strengthen it.
func TestTarpit_SlowsACallerAndNeverQueuesOneForLong(t *testing.T) {
	const (
		delay    = 80 * time.Millisecond
		parallel = 12
	)
	rl := authmw.NewRateLimiter(1, time.Minute)
	rl.Allow("victim@example.com") // spend the budget, so the key is over it

	var (
		mu      sync.Mutex
		longest time.Duration
		delayed int
		wg      sync.WaitGroup
	)
	start := make(chan struct{})
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			began := time.Now()
			rl.Tarpit(context.Background(), "victim@example.com", delay)
			took := time.Since(began)

			mu.Lock()
			defer mu.Unlock()
			if took > longest {
				longest = took
			}
			// Everything over budget waits, whether it gets a turn or gives
			// up waiting for one. Nothing sails straight through.
			if took >= delay*9/10 {
				delayed++
			}
		}()
	}

	close(start)
	wg.Wait()

	require.Equal(t, parallel, delayed,
		"%d of %d callers were not slowed at all", parallel-delayed, parallel)

	// One wait for a turn, one hold. Anything longer is a queue an attacker
	// can lengthen, which is how the account's owner gets held out — the
	// failure the first version of this had.
	require.Less(t, longest, 4*delay,
		"one caller waited %v against a delay of %v, so a flood can hold somebody's own login open",
		longest, delay)
}

// A serial caller is slowed once per attempt, which is the case the delay
// actually buys something in. Ten attempts at the delay cannot finish in less
// than nine of them.
//
// Deliberately not asserted for parallel callers. A request that cannot get
// its turn within the delay proceeds without one, so with N arriving together
// the bound is N over the delay rather than one over it — that is the trade
// made to guarantee nobody is ever refused, and Tarpit carries the
// measurements. A test that asserted strict serialisation under concurrency
// would be asserting a coin flip: whether a waiter grabs the released turn or
// times out first is a race between two timers set to the same duration, and
// the earlier version of this test failed about once in four runs because of
// it.
func TestTarpit_SlowsASerialCaller(t *testing.T) {
	const (
		delay    = 20 * time.Millisecond
		attempts = 10
	)
	rl := authmw.NewRateLimiter(1, time.Minute)
	rl.Allow("victim@example.com")

	began := time.Now()
	for range attempts {
		rl.Tarpit(context.Background(), "victim@example.com", delay)
	}
	elapsed := time.Since(began)

	require.GreaterOrEqual(t, elapsed, time.Duration(attempts-1)*delay,
		"%d sequential attempts finished in %v, so the delay is not being taken",
		attempts, elapsed)
}

// A key inside its budget is not delayed at all, and neither is anything when
// the limiter is switched off.
func TestTarpit_DoesNothingWhenItShouldNot(t *testing.T) {
	const delay = 200 * time.Millisecond

	t.Run("the limiter is off", func(t *testing.T) {
		rl := authmw.NewRateLimiter(0, time.Minute)
		began := time.Now()
		rl.Tarpit(context.Background(), "k", delay)
		require.Less(t, time.Since(began), delay)
	})

	t.Run("the delay is zero", func(t *testing.T) {
		rl := authmw.NewRateLimiter(1, time.Minute)
		began := time.Now()
		rl.Tarpit(context.Background(), "k", 0)
		require.Less(t, time.Since(began), 50*time.Millisecond)
	})

	t.Run("a cancelled request does not sit out its delay", func(t *testing.T) {
		rl := authmw.NewRateLimiter(1, time.Minute)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		began := time.Now()
		rl.Tarpit(ctx, "k", delay)
		require.Less(t, time.Since(began), delay,
			"a client that has gone away should not hold a turn")
	})
}

// Exceeded answers the question without spending an attempt, which is what
// lets the handler ask it before doing the expensive part.
func TestExceeded_DoesNotCount(t *testing.T) {
	rl := authmw.NewRateLimiter(2, time.Minute)

	require.False(t, rl.Exceeded("k"))
	for range 20 {
		require.False(t, rl.Exceeded("k"), "asking the question spent an attempt")
	}

	require.True(t, rl.Allow("k"))
	require.False(t, rl.Exceeded("k"))
	require.True(t, rl.Allow("k"))
	require.True(t, rl.Exceeded("k"), "the budget is spent and Exceeded says otherwise")

	rl.Reset("k")
	require.False(t, rl.Exceeded("k"), "a correct password should clear the budget")
}
