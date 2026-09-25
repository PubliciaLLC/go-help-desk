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
func TestTarpit_IsSequentialAndBounded(t *testing.T) {
	const (
		delay    = 80 * time.Millisecond
		parallel = 12
	)
	rl := authmw.NewRateLimiter(1, time.Minute)
	rl.Allow("victim@example.com") // spend the budget, so the key is over it

	var (
		mu      sync.Mutex
		longest time.Duration
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
		}()
	}

	began := time.Now()
	close(start)
	wg.Wait()
	elapsed := time.Since(began)

	// Sequential: the first caller holds its turn for the whole delay, so the
	// second cannot even start one until then — two turns, end to end, before
	// the last caller is done. Callers that ran in parallel would all be
	// finished in a little over one delay, which is what this catches.
	require.Greater(t, elapsed, delay*3/2,
		"%d calls finished in %v against a delay of %v, so the waits ran in parallel and bound nothing",
		parallel, elapsed, delay)

	// Bounded: one wait for a turn, one hold. Anything longer is a queue an
	// attacker can lengthen, which is how the owner gets held out.
	require.Less(t, longest, 4*delay,
		"one caller waited %v against a delay of %v, so a flood can hold somebody's own login open",
		longest, delay)
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
