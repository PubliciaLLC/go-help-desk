package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The limiter having correct arithmetic proves nothing about whether the
// credential routes are wired to it. This drives the real router.
//
// Before this, nothing throttled login or TOTP verification: a six-digit code
// is ~500k guesses on average, and totp.Validate accepts a ±1 window, so three
// windows' codes are live at once.
func TestAuthRateLimit_AppliesToCredentialRoutes(t *testing.T) {
	h, cleanup := newHarnessWithRateLimit(t, 3)
	defer cleanup()

	badLogin := func() int {
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
			"email": "staff@test.local", "password": "wrong-password",
		})
		res.Body.Close()
		return res.StatusCode
	}

	// Three wrong guesses are answered normally.
	for i := 1; i <= 3; i++ {
		require.Equal(t, http.StatusUnauthorized, badLogin(), "attempt %d", i)
	}
	// The fourth is refused by the throttle rather than reaching bcrypt.
	require.Equal(t, http.StatusTooManyRequests, badLogin(),
		"login must be throttled once the limit is spent")

	// The same budget covers MFA verification, which is the guessable one.
	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": "000000"})
	res.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode)
}

// Routes that are not guessable secrets must not be throttled — notably the
// SSO callbacks, where a legitimate burst follows an IdP redirect.
func TestAuthRateLimit_LeavesNonCredentialRoutesAlone(t *testing.T) {
	h, cleanup := newHarnessWithRateLimit(t, 1)
	defer cleanup()

	for i := 0; i < 5; i++ {
		res := h.doUnauth(t, http.MethodGet, "/api/v1/auth/providers", nil)
		res.Body.Close()
		require.NotEqual(t, http.StatusTooManyRequests, res.StatusCode,
			"provider discovery is not a credential endpoint")
	}
}
