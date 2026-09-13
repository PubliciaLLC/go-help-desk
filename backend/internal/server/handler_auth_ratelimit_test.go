package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
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

// The endpoint the whole exercise is about: six digits, three codes live at
// once. Keyed on the authenticated user, so no proxy topology and no IP
// rotation affects it.
func TestMFAVerifyThrottle_IsKeyedOnTheUser(t *testing.T) {
	h, cleanup := newHarness(t) // login throttle off; the MFA budget is not configurable
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["staff","admin"]`)))
	enrollMFA(t, ctx, h.userSvc, h.staffID)

	s := &session{h: h}
	res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode)

	// mfaAttemptLimit is 5.
	for i := 1; i <= 5; i++ {
		res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
			map[string]any{"code": fmt.Sprintf("%06d", i)})
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, "guess %d", i)
	}

	res, _ = s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": "999999"})
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode,
		"a six-digit code must not be guessable at will")

	// And a forged header buys nothing, because the key is the user.
	res, _ = s.sendWithHeaders(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
		map[string]any{"code": "888888"}, map[string]string{"X-Forwarded-For": "1.2.3.4"})
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode,
		"the budget follows the account, not the address")
}

// A legitimate user who fumbles a code and then enters the right one must not
// stay throttled.
func TestMFAVerifyThrottle_SuccessClearsTheBudget(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["staff","admin"]`)))
	enrollMFA(t, ctx, h.userSvc, h.staffID)

	u, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)

	s := &session{h: h}
	s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})

	for i := 0; i < 3; i++ {
		s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": "000000"})
	}

	code, err := totp.GenerateCode(u.MFASecret, time.Now())
	require.NoError(t, err)
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": code})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
}
