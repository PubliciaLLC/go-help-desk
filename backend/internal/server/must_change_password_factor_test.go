package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Reviewer repro (#498): the flag in the database does not mean "no factor".
// A session that did not pick the flag up (SSO sign-in, which never reads it,
// stands in here as a session opened before the flag was set) can enrol TOTP.
// After that, the password alone must not be enough to change the password.
func TestMustChangePassword_DoesNotLetAPasswordOnlySessionSkipAnEnrolledFactor(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	mfaOptionalForStaff(t, h)

	unflagged := staffPasswordLogin(t, h)
	_, err := h.tx.ExecContext(ctx, `UPDATE users SET must_change_password = true WHERE id = $1`, h.staffID)
	require.NoError(t, err)
	enrolTOTP(t, unflagged)

	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	require.Contains(t, string(body), `"mfa_needed":true`)

	res, body = s.send(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"current_password": "password", "new_password": "attacker-chosen-password"})
	require.Equal(t, http.StatusForbidden, res.StatusCode,
		"password changed without the enrolled TOTP being passed: %s", body)
}
