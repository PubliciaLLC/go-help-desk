package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/stretchr/testify/require"
)

// The limiter's own unit tests passed while the deployed chain was fully
// bypassable: chi's RealIP middleware ran first and overwrote RemoteAddr from
// X-Forwarded-For, so the limiter read a value the caller chose. Twenty forged
// headers, zero refusals. These tests drive the REAL router, which is the only
// way that class of bug is visible.
//
// The keys are accounts now, not addresses, so a forged header is irrelevant
// by construction — but the test stays, because the next person to reach for a
// client address needs it to fail.

func TestLoginThrottle_IsKeyedOnTheAccount(t *testing.T) {
	h, cleanup := newHarnessWithRateLimit(t, 3)
	defer cleanup()

	badLogin := func(email string, headers map[string]string) int {
		res := h.doUnauthWithHeaders(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": email, "password": "wrong-password"}, headers)
		res.Body.Close()
		return res.StatusCode
	}

	for i := 1; i <= 3; i++ {
		require.Equal(t, http.StatusUnauthorized, badLogin("staff@test.local", nil), "attempt %d", i)
	}
	require.Equal(t, http.StatusTooManyRequests, badLogin("staff@test.local", nil),
		"the account's budget is spent")

	t.Run("a forged forwarding header does not mint a fresh budget", func(t *testing.T) {
		for i, xff := range []string{"1.2.3.4", "5.6.7.8", "9.9.9.9"} {
			require.Equal(t, http.StatusTooManyRequests,
				badLogin("staff@test.local", map[string]string{"X-Forwarded-For": xff, "X-Real-IP": xff}),
				"forged header %d must not reset the budget", i)
		}
	})

	t.Run("another account is unaffected", func(t *testing.T) {
		require.Equal(t, http.StatusUnauthorized, badLogin("someone-else@test.local", nil),
			"one account's spending must not throttle everyone")
	})

	// 429-versus-401 must not reveal which addresses have accounts, so a
	// nonexistent address is counted the same way.
	t.Run("a nonexistent account is throttled identically", func(t *testing.T) {
		ghost := "ghost@test.local"
		for i := 1; i <= 3; i++ {
			require.Equal(t, http.StatusUnauthorized, badLogin(ghost, nil), "attempt %d", i)
		}
		require.Equal(t, http.StatusTooManyRequests, badLogin(ghost, nil))
	})
}

// A correct password clears the budget: NIST SP 800-63B disregards prior
// failures on success.
func TestLoginThrottle_SuccessClearsTheBudget(t *testing.T) {
	h, cleanup := newHarnessWithRateLimit(t, 3)
	defer cleanup()

	for i := 0; i < 2; i++ {
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "staff@test.local", "password": "wrong"})
		res.Body.Close()
	}

	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	// Full budget again rather than one remaining attempt.
	for i := 1; i <= 3; i++ {
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "staff@test.local", "password": "wrong"})
		res.Body.Close()
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"attempt %d should still be inside the refreshed budget", i)
	}
}

// TOTP throttling moved to a durable per-account counter; see
// TestMFALock_IsDurable in handler_auth_hardening_test.go. Keeping an
// in-memory version here too would have meant two limits on one secret, the
// weaker of which resets on restart.

// The first version of this throttle refused on the count BEFORE checking the
// password, which let any anonymous caller lock any account out of its own
// login just by knowing the email address. Verified before the fix: three
// wrong guesses, then the real user's correct password answered 429.
func TestLoginThrottle_CannotLockAUserOutOfTheirOwnAccount(t *testing.T) {
	h, cleanup := newHarnessWithRateLimit(t, 3)
	defer cleanup()

	for i := 0; i < 6; i++ {
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "staff@test.local", "password": "attacker guess"})
		res.Body.Close()
	}

	// Different casing and padding, to confirm it normalises to the same
	// bucket and is still admitted.
	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "  STAFF@test.local ", "password": "password"})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode,
		"a correct password must be honoured however much an attacker has spent")
}

// Wrong guesses are still capped once the budget is gone.
func TestLoginThrottle_RefusesSustainedWrongGuesses(t *testing.T) {
	h, cleanup := newHarnessWithRateLimit(t, 3)
	defer cleanup()

	bad := func() int {
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "staff@test.local", "password": "wrong"})
		res.Body.Close()
		return res.StatusCode
	}
	for i := 1; i <= 3; i++ {
		require.Equal(t, http.StatusUnauthorized, bad(), "attempt %d", i)
	}
	require.Equal(t, http.StatusTooManyRequests, bad())
}

// Signup is the one endpoint with no account to key on, so it keys on the
// transport address. It had no test at all.
func TestSignupThrottle_IsApplied(t *testing.T) {
	h, cleanup := newHarnessWithRateLimit(t, 2)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))

	signup := func(email string) int {
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup",
			map[string]any{"email": email, "display_name": "X", "password": "a-long-enough-password"})
		res.Body.Close()
		return res.StatusCode
	}
	signup("a@test.local")
	signup("b@test.local")
	require.Equal(t, http.StatusTooManyRequests, signup("c@test.local"),
		"signup must be throttled per source address")
}
