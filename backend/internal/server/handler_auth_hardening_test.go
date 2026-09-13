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

// A session that has passed the password but not the second factor should
// reach nothing but the challenge it still owes.
func TestHalfAuthenticatedSession_CannotReadReferenceData(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["staff","admin"]`)))
	enrollMFA(t, ctx, h.userSvc, h.staffID)

	s := &session{h: h}
	res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode)

	for _, path := range []string{"/api/v1/tags", "/api/v1/statuses"} {
		t.Run(path, func(t *testing.T) {
			res, body := s.send(t, http.MethodGet, path, nil)
			require.Equal(t, http.StatusForbidden, res.StatusCode,
				"reachable before the MFA challenge was answered; body %s", body)
		})
	}
}

// The TOTP budget lives on the user row, not in process memory. That is the
// whole point: an in-memory counter is cleared by a restart and kept per
// process, so N replicas multiply it by N — which is not a limit on a
// six-digit secret that a password-holder can grind at.
func TestMFALock_IsDurable(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["staff","admin"]`)))
	enrollMFA(t, ctx, h.userSvc, h.staffID)

	s := &session{h: h}
	res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode)

	// user.MFAMaxFailedAttempts is 5.
	for i := 1; i <= 5; i++ {
		res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
			map[string]any{"code": fmt.Sprintf("%06d", i)})
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, "guess %d", i)
	}

	res, _ = s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": "999999"})
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode, "the budget is spent")

	// The lock is a column, so it is still there for anything reading the row —
	// a restarted process, or another replica.
	require.Error(t, h.userSvc.CheckMFALock(ctx, h.staffID),
		"the lock must live in the database, not in the process that recorded it")

	// A correct code is still refused while locked: the lock gates BEFORE
	// verification, or an attacker gets unlimited attempts by guessing right.
	u, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)
	code, err := totp.GenerateCode(u.MFASecret, time.Now())
	require.NoError(t, err)
	res, _ = s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": code})
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode,
		"a locked account must not be openable even with the right code")
}

// A user who fumbles a code and then gets it right must not stay counted, per
// NIST SP 800-63B.
func TestMFALock_ClearedOnSuccess(t *testing.T) {
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

	for i := 0; i < 4; i++ {
		s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": "000000"})
	}

	code, err := totp.GenerateCode(u.MFASecret, time.Now())
	require.NoError(t, err)
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": code})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	require.NoError(t, h.userSvc.CheckMFALock(ctx, h.staffID),
		"prior failures must be forgotten after a correct code")

	// Not just unlocked — the budget must be FULL again. Asserting only that
	// the account is unlocked passes even if the counter was never cleared,
	// which is how the earlier version of this test passed with the clear
	// removed entirely.
	for i := 1; i <= 4; i++ {
		res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": "000001"})
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"failure %d should be inside a refreshed budget, not a carried-over one", i)
	}
}
