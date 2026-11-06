package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// An account the server console unlocked carries must_change_password: the
// password it was handed is known to whoever ran the command, so the session
// it opens may do one thing, which is replace it (#497).
//
// Every route listed here sits behind RequireMFA or is one of the two
// enrolment doors outside it. A flagged session must be refused by all of
// them, with a code the frontend can act on.
func TestMustChangePassword_OnlyThePasswordChangeIsAllowedUntilItIsDone(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	_, err := h.tx.ExecContext(ctx, `UPDATE users SET must_change_password = true WHERE id = $1`, h.adminID)
	require.NoError(t, err)

	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "the password must still sign in: %s", body)
	require.Contains(t, string(body), `"password_change_required":true`)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/me"},
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodGet, "/api/v1/admin/settings"},
		{http.MethodGet, "/api/v1/tickets"},
		{http.MethodPost, "/api/v1/me/mfa/enroll"},
		{http.MethodPost, "/api/v1/me/passkeys/register/start"},
	} {
		res, body = s.send(t, route.method, route.path, nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode, "%s %s was reachable: %s", route.method, route.path, body)
		require.Contains(t, string(body), "password_change_required", "%s %s", route.method, route.path)
	}

	// The current password is still asked for: a stolen cookie is not enough.
	res, _ = s.send(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"current_password": "wrong", "new_password": "a-brand-new-password"})
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	res, body = s.send(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"current_password": "password", "new_password": "a-brand-new-password"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "changing the password must be allowed: %s", body)

	u, err := h.userSvc.GetByIDAdmin(ctx, h.adminID)
	require.NoError(t, err)
	require.False(t, u.MustChangePassword, "the flag survived the change")

	// The same session is now ordinary.
	res, body = s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "still restricted after the change: %s", body)

	// And so is the next sign-in.
	again := &session{h: h}
	res, body = again.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "a-brand-new-password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	require.NotContains(t, string(body), `"password_change_required":true`)
}

// The flag must never make an account unable to sign in (.claude/CLAUDE.md,
// last-administrator guard). The hazard here is a deadlock: an administrator
// whose role must enrol MFA is held at "enrol first" by RequireMFA and at
// "change your password first" by the flag. The password change is therefore
// reachable by a session that has not passed MFA, and enrolment follows it.
func TestMustChangePassword_DoesNotLockOutAnAccountThatOwesEnrolment(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	requireSoleAdministrator(t, h)

	setup := loggedInAdmin(t, h)
	res, body := setup.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"mfa_enabled": true, "mfa_enforced_roles": []string{"admin"},
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
	require.NoError(t, h.userSvc.ResetMFA(ctx, h.adminID, nil))
	_, err := h.tx.ExecContext(ctx, `UPDATE users SET must_change_password = true WHERE id = $1`, h.adminID)
	require.NoError(t, err)

	s := &session{h: h}
	res, body = s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	require.Contains(t, string(body), `"mfa_enrollment_needed":true`)
	require.Contains(t, string(body), `"password_change_required":true`)

	res, _ = s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "enrolment ran before the password was changed")

	res, body = s.send(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"current_password": "password", "new_password": "a-brand-new-password"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "deadlock: cannot change the password without MFA: %s", body)

	// Still owes the enrolment, and can now do it.
	res, body = s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "the account cannot reach enrolment after the change: %s", body)
	require.Contains(t, string(body), "secret")
}
