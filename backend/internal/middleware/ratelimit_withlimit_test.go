package middleware_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// TakeWithLimit takes its budget from the caller, so a limit an administrator
// edits applies on the next call; SetClock lets the window pass without
// sleeping (#445).
func TestRateLimiter_TakeWithLimitAndInjectedClock(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	rl := authmw.NewRateLimiter(1, time.Hour)
	rl.SetClock(func() time.Time { return now })

	for i := range 2 {
		ok, _ := rl.TakeWithLimit("a", 2)
		require.True(t, ok, "take %d of 2", i+1)
	}
	ok, _ := rl.TakeWithLimit("a", 2)
	require.False(t, ok, "third is over a limit of 2")
	ok, _ = rl.TakeWithLimit("b", 2)
	require.True(t, ok, "another key has its own count")
	require.Equal(t, time.Hour, rl.Remaining())

	// Raising the limit applies at once: the same count is now within it.
	ok, _ = rl.TakeWithLimit("a", 10)
	require.True(t, ok)

	now = now.Add(30 * time.Minute)
	require.Equal(t, 30*time.Minute, rl.Remaining())
	now = now.Add(31 * time.Minute)
	ok, _ = rl.TakeWithLimit("a", 2)
	require.True(t, ok, "a new window")
	ok, _ = rl.TakeWithLimit("a", 0)
	require.True(t, ok, "zero means no limit")
}
