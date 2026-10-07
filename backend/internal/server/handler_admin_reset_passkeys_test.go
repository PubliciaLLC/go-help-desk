package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// "Reset MFA" cleared the TOTP columns and nothing else (#307 item 2). On an
// account whose only factor was a passkey the administrator pressed it, got a
// success, and the person was still asked for the key they had lost: the only
// way out was the reset-factors command on the server.
//
// Driven the way the button is: PATCH with reset_mfa, then the account's own
// next sign-in, which is where "still locked out" shows.
func TestAdminResetMFA_ClearsAPasskeyOnlyAccount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	admin := loggedInAdmin(t, h)
	res, body := admin.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"mfa_enabled": true, "mfa_enforced_roles": []string{"user"},
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)

	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.userID, "the-lost-key")))
	stored, err := h.userSvc.GetByIDAdmin(ctx, h.userID)
	require.NoError(t, err)
	require.False(t, stored.MFAEnabled, "precondition: a passkey-only account has no TOTP")

	signIn := func(t *testing.T) map[string]any {
		t.Helper()
		s := &session{h: h}
		res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "user@test.local", "password": "password"})
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		return out
	}
	before := signIn(t)
	require.Equal(t, true, before["passkey_needed"], "precondition: the lost key is being asked for")

	res, body = admin.send(t, http.MethodPatch, "/api/v1/admin/users/"+h.userID.String(),
		map[string]any{"reset_mfa": true})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)

	n, err := h.passkeyStore.CountForUser(ctx, h.userID)
	require.NoError(t, err)
	require.Zero(t, n, "Reset MFA reported success and the passkey is still registered")

	after := signIn(t)
	require.Equal(t, false, after["passkey_needed"],
		"the person is still asked for the key the administrator reset")
	require.Equal(t, true, after["mfa_enrollment_needed"],
		"after a reset they must be asked to enrol again, not left with no way forward")

	entries, err := h.auditStore.ListByEntity(ctx, "user", h.userID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.EqualValues(t, 1, entries[0].After["passkeys_removed"],
		"the audit entry must say the reset removed a passkey")
}

// #338: the confirm route's last-mile write refuses a first enrolment once the
// account is protected. Reaching the refusal over HTTP needs the rival to land
// between the route's guard and its write, which this harness (one connection,
// one request at a time) cannot interleave, so the write is exercised where
// the window is: TestSetMFAIfNotEnabled for the SQL and
// TestConfirmMFAEnrollmentWith_* for the service. What this test pins is the
// route's two behaviours that ARE reachable and must not change: a first
// enrolment still completes, and a rotation by a session that proved the
// existing factor still overwrites.
func TestEnrollConfirm_FirstEnrolmentAndRotationStillWork(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	enrol := func(t *testing.T, s *session) (secret string) {
		t.Helper()
		res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
		var out struct {
			Secret string `json:"secret"`
		}
		require.NoError(t, json.Unmarshal(body, &out))
		require.NotEmpty(t, out.Secret)
		return out.Secret
	}
	confirm := func(t *testing.T, s *session, secret string) *http.Response {
		t.Helper()
		code, err := totp.GenerateCode(secret, time.Now())
		require.NoError(t, err)
		res, _ := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll/confirm", map[string]any{"code": code})
		return res
	}

	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "user@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)

	first := enrol(t, s)
	res = confirm(t, s, first)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "a first enrolment must still complete")

	u, err := h.userSvc.GetByIDAdmin(context.Background(), h.userID)
	require.NoError(t, err)
	require.True(t, u.MFAEnabled)
	require.Equal(t, first, u.MFASecret)

	// The session that just enrolled has proved the factor, so it may rotate.
	second := enrol(t, s)
	res = confirm(t, s, second)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "rotation by a verified session must still work")
	u, err = h.userSvc.GetByIDAdmin(context.Background(), h.userID)
	require.NoError(t, err)
	require.Equal(t, second, u.MFASecret, "the verified session could no longer rotate its own factor")
}
