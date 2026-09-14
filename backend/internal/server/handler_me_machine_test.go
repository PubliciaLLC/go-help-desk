package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// An API key acts at its owner's identity. Before this guard a leaked key was
// full account takeover rather than the access it was issued for: change the
// owner's password, re-enroll MFA against an attacker's authenticator, and the
// human is locked out of their own account by a credential they created for a
// cron job. Revoking the key afterwards undoes neither.
//
// Scopes cannot solve this. The narrowest possible key still belongs to its
// owner, so any scope reaching these routes reaches account takeover.
func TestMachineCredential_CannotTakeOverTheAccount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"change the owner's password", http.MethodPatch, "/api/v1/me/password",
			map[string]any{"password": "attacker-chosen-password-123"}},
		{"start MFA re-enrollment", http.MethodGet, "/api/v1/me/mfa/enroll", nil},
		{"confirm MFA re-enrollment", http.MethodPost, "/api/v1/me/mfa/enroll/confirm",
			map[string]any{"code": "000000"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := h.do(t, tc.method, tc.path, tc.body)
			require.Equal(t, http.StatusForbidden, resp.StatusCode,
				"a machine credential must not reach %s", tc.path)

			var body struct {
				Error struct{ Code string } `json:"error"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			require.Equal(t, "session_required", body.Error.Code)
		})
	}
}

// The password must be unchanged afterwards, not merely refused at the door.
func TestMachineCredential_PasswordChangeDoesNotTakeEffect(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"password": "attacker-chosen-password-123"})
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// The staff user's original password still works. loggedIn asserts the
	// login succeeds, so it failing here would mean the change took effect.
	loggedIn(t, h)
}

// A person signed in through the browser must still be able to do all of this —
// a guard that locked everyone out would pass the tests above.
func TestSession_CanStillManageItsOwnAccount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	s := loggedIn(t, h)

	res, body := s.send(t, http.MethodGet, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode,
		"a signed-in person must still be able to enroll MFA; body: %s", body)
}

// Reading your own identity stays available: an integration legitimately needs
// to know who it is acting as.
func TestMachineCredential_CanStillReadItsOwnIdentity(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// Refusing only the credential's OWN owner closed the path and not the outcome.
// Every key reaching /admin is administrator-owned, so with users:write the long
// way round was four requests: create a second administrator, sign in as them,
// reset the original owner's password.
//
// The rule is therefore about administrators, not about self.
func TestMachineCredential_CannotReachAdministratorCredentials(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("cannot create an administrator", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
			"email": "second-admin@test.local", "display_name": "Second",
			"role": "admin", "password": "a-password-123",
		})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("cannot promote to administrator", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodPatch,
			"/api/v1/admin/users/"+h.staffID.String(), map[string]any{"role": "admin"})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("cannot reset an administrator's password", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodPost,
			"/api/v1/admin/users/"+h.adminID.String()+"/password",
			map[string]any{"new_password": "attacker-chosen-password-123"})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)

		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.Equal(t, "session_required", body.Error.Code)
	})

	t.Run("cannot reset an administrator's MFA", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodPatch,
			"/api/v1/admin/users/"+h.adminID.String(), map[string]any{"reset_mfa": true})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}

// Ordinary automation must keep working: provisioning and deprovisioning
// non-administrators, and resetting a forgotten password.
func TestMachineCredential_CanStillAdministerNonAdmins(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "new-agent@test.local", "display_name": "Agent",
		"role": "staff", "password": "a-password-123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode,
		"creating a non-administrator is ordinary automation")

	resp = h.doAsAdmin(t, http.MethodPost,
		"/api/v1/admin/users/"+h.staffID.String()+"/password",
		map[string]any{"new_password": "a-legitimate-reset-123"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode,
		"resetting a non-administrator's password is ordinary automation")
}

// A signed-in administrator must still be able to do all of it. A guard keyed on
// the target rather than the caller would lock the humans out too.
func TestSession_CanStillAdministerAdministrators(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	res, body = sess.send(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "third-admin@test.local", "display_name": "Third",
		"role": "admin", "password": "a-password-123",
	})
	require.Equal(t, http.StatusCreated, res.StatusCode,
		"a signed-in administrator must still create administrators; body: %s", body)

	res, body = sess.send(t, http.MethodPost,
		"/api/v1/admin/users/"+h.adminID.String()+"/password",
		map[string]any{"new_password": "a-deliberate-reset-123"})
	require.Equal(t, http.StatusNoContent, res.StatusCode,
		"and reset an administrator's password; body: %s", body)
}

// The escalation chain the review demonstrated, end to end: it must fail at the
// first step rather than merely being inconvenient.
func TestMachineCredential_EscalationChainIsBlockedAtStepOne(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "pivot@test.local", "display_name": "Pivot",
		"role": "admin", "password": "attacker-known-password-123",
	})
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"step one of the chain must fail")

	// And the account must not exist, so no later step has anything to use.
	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/users", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var users []struct {
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&users))
	for _, u := range users {
		require.NotEqual(t, "pivot@test.local", u.Email,
			"the refused administrator must not have been created")
	}
}
