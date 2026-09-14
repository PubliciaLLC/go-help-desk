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

// /me/password is closed to machine credentials, but an admin-owned API key can
// reach the same outcome the long way round: reset the password of user {id}
// where {id} is its own owner. In practice every API key is admin-owned,
// because only admins can mint one and it acts at its owner's role.
//
// Resetting OTHER users' credentials is ordinary administrative automation and
// must keep working.
func TestMachineCredential_CannotResetItsOwnOwnersCredentials(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// h.adminKey belongs to the seeded admin, so adminID is its own owner.
	self := "/api/v1/admin/users/" + h.adminID.String()

	resp := h.doAsAdmin(t, http.MethodPost, self+"/password",
		map[string]any{"new_password": "attacker-chosen-password-123"})
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a key must not reset the password of the account it belongs to")

	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "session_required", body.Error.Code)

	resp = h.doAsAdmin(t, http.MethodPatch, self, map[string]any{"reset_mfa": true})
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"nor reset its own owner's MFA")
}

func TestMachineCredential_CanStillAdministerOtherUsers(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// The staff user is a different account from the admin key's owner.
	other := "/api/v1/admin/users/" + h.staffID.String()

	resp := h.doAsAdmin(t, http.MethodPost, other+"/password",
		map[string]any{"new_password": "a-legitimate-reset-123"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode,
		"resetting another user's password is ordinary admin automation")
}

// A signed-in admin must still be able to reset their own credentials — the
// guard is about machine credentials, not about self-service.
func TestSession_CanStillResetItsOwnPassword(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	sess := loggedIn(t, h)
	res, body := sess.send(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"password": "a-new-password-123"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
}
