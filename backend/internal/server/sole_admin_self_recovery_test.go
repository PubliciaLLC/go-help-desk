package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The only administrator, with no working second factor, can get back in
// without anyone's help.
//
// This is the mechanism that lets the last-administrator guard stay as it is.
// That guard refuses to disable, demote or delete the last active
// administrator because setup does not reopen — an instance with no
// administrator is one nobody can administer again. "Removed their last way
// in" is the same mistake in different clothes, and the obvious worry is that
// clearing the sole administrator's MFA is exactly that.
//
// It is not, and the reason is structural rather than lucky: enrolment lives
// OUTSIDE RequireMFA in meRouter, with the comment "otherwise a user
// compelled to enroll cannot complete enrollment". So the password still
// works, the session comes back with MFAPassed false, every protected route
// refuses it — and enrolment is still reachable, so they mint a new factor
// themselves.
//
// Written before any passkey code, deliberately. It pins the property that
// makes deferring a fourth case in the guard correct rather than merely
// convenient, and it is the test the passkey registration routes will have to
// satisfy too: if they cannot be reached this way, the guard needs to learn
// about credentials before passwordless sign-in ships, not after.
//
// A guarantee that holds only because some code path happens not to exist yet
// is the mistake found in the last-admin statements themselves, where the
// target row was unfiltered and safe only because nothing restored a deleted
// user. This is the other kind: it holds because of something that is here
// and tested.
func TestSoleAdministrator_CanSelfRecoverWithNoSecondFactor(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// This instance demands MFA of its administrators. Both settings: the
	// global toggle alone is not enforcement — MFARequiredFor also wants the
	// role named in the enforced list, which is what turns a fresh session
	// into one that owes a factor before it may do anything.
	// Through the settings route an administrator would actually use.
	// adminSvc.SetString JSON-encodes the STRING, so it stores "[\"admin\"]"
	// rather than a list, and MFAEnforcedRoles then parses nothing.
	// A signed-in session, not the API key: mfa_enabled is auth-critical, and
	// handleUpdateSettings refuses a machine credential outright — turning MFA
	// off instance-wide is a route to becoming somebody rather than acting for
	// them.
	setup := loggedInAdmin(t, h)
	setRes, setBody := setup.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"mfa_enabled": true, "mfa_enforced_roles": []string{"admin"},
	})
	require.Equal(t, http.StatusNoContent, setRes.StatusCode,
		"could not turn MFA enforcement on: %s", setBody)
	require.True(t, h.adminSvc.MFARequiredFor(ctx, "admin"),
		"precondition: this instance must actually demand MFA of administrators")

	// The sole administrator, whose authenticator is gone — lost phone, or an
	// administrator (themselves) having cleared it.
	require.NoError(t, h.userSvc.ResetMFA(ctx, h.adminID, nil))
	stored, err := h.userSvc.GetByIDAdmin(ctx, h.adminID)
	require.NoError(t, err)
	require.False(t, stored.MFAEnabled, "precondition: no working second factor")
	require.Equal(t, user.RoleAdmin, stored.Role)

	// They can still sign in with the password.
	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode,
		"the password should still work: %s", body)

	// And that session reaches nothing, because it owes a second factor.
	// The session is not fully authenticated: it owes an enrolment, and every
	// protected route refuses it until that is done.
	res, _ = s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode,
		"a session that still owes an enrolment reached a protected route")

	// The whole point: enrolment is still open to them.
	res, body = s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode,
		"the sole administrator cannot reach enrolment, so nobody can recover this instance: %s", body)
	require.Contains(t, string(body), "secret",
		"enrolment answered without a secret to enrol with: %s", body)
}
