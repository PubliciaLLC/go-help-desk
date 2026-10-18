package middleware_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// Remaining is what a 429's Retry-After is built from: the time until the
// window that refused the caller ends, not the length of a whole window (#421).
func TestRateLimiter_Remaining(t *testing.T) {
	rl := authmw.NewRateLimiter(1, 2*time.Second)
	require.LessOrEqual(t, rl.Remaining(), 2*time.Second)
	require.Greater(t, rl.Remaining(), time.Second)

	first := rl.Remaining()
	time.Sleep(150 * time.Millisecond)
	require.Less(t, rl.Remaining(), first, "the time remaining must fall as the window runs out")

	// A limiter with nothing to wait for reports nothing.
	require.Zero(t, authmw.NewRateLimiter(0, time.Minute).Remaining())
	// And never a negative duration, whatever the window did.
	expired := authmw.NewRateLimiter(1, time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	require.Zero(t, expired.Remaining())
}
