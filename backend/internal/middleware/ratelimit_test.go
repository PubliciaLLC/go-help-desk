package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

func TestRateLimiter(t *testing.T) {
	t.Run("allows up to the limit then refuses", func(t *testing.T) {
		rl := authmw.NewRateLimiter(3, time.Minute)
		for i := 1; i <= 3; i++ {
			require.True(t, rl.Allow("k"), "attempt %d", i)
		}
		require.False(t, rl.Allow("k"), "the attempt past the limit must be refused")
	})

	t.Run("counts per key", func(t *testing.T) {
		rl := authmw.NewRateLimiter(1, time.Minute)
		require.True(t, rl.Allow("a"))
		require.False(t, rl.Allow("a"))
		require.True(t, rl.Allow("b"), "one key's spending must not charge another")
	})

	// NIST SP 800-63B has the verifier disregard prior failures once the user
	// authenticates successfully; without it, two fumbled codes followed by a
	// correct one still leave the user throttled for the rest of the window.
	t.Run("success clears the budget", func(t *testing.T) {
		rl := authmw.NewRateLimiter(2, time.Minute)
		require.True(t, rl.Allow("k"))
		rl.Reset("k")
		require.True(t, rl.Allow("k"))
		require.True(t, rl.Allow("k"), "the budget must be the full one again")
	})

	t.Run("the window resets", func(t *testing.T) {
		rl := authmw.NewRateLimiter(1, 30*time.Millisecond)
		require.True(t, rl.Allow("k"))
		require.False(t, rl.Allow("k"))
		require.Eventually(t, func() bool { return rl.Allow("k") },
			time.Second, 10*time.Millisecond, "the window must expire and allow again")
	})

	t.Run("a limit of zero disables it", func(t *testing.T) {
		rl := authmw.NewRateLimiter(0, time.Minute)
		for i := 0; i < 50; i++ {
			require.True(t, rl.Allow("k"))
		}
		rl.Reset("k") // must not panic
	})
}

// ClientAddr is used only where no account key exists. It must never read a
// forwarding header: that is attacker controlled, and honouring it would let
// one client mint a fresh identity per request.
func TestClientAddr_IgnoresForwardingHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "5.6.7.8")
	req.Header.Set("True-Client-IP", "9.9.9.9")

	require.Equal(t, "10.0.0.1", authmw.ClientAddr(req),
		"the transport address, not anything the caller asked us to believe")
}

// The port changes per connection; keying on the full RemoteAddr would hand an
// attacker a fresh budget per request.
func TestClientAddr_StripsThePort(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.9:1111"
	a := authmw.ClientAddr(req)
	req.RemoteAddr = "10.0.0.9:2222"
	require.Equal(t, a, authmw.ClientAddr(req))
}
