package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// "Also revoke this person's API keys" on an administrator's password reset
// (#450). Unticked by default. Only an administrator resetting someone else's
// password can do it; nothing else touches keys.

func (h *harness) resetPassword(t *testing.T, cookie string, id uuid.UUID, body map[string]any) (int, []byte) {
	t.Helper()
	res := h.doSession(t, cookie, http.MethodPost, "/api/v1/admin/users/"+id.String()+"/password", body)
	b := readAll(t, res)
	return res.StatusCode, b
}

func (h *harness) keyCount(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	keys, err := h.authStore.ListByUser(context.Background(), userID)
	require.NoError(t, err)
	return len(keys)
}

func TestAdminPasswordReset_LeavesKeysAloneUnlessAsked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := h.session(t, asAdmin)
	before := h.keyCount(t, h.staffID)
	require.Equal(t, 1, before, "the harness gives staff one key")

	for name, body := range map[string]map[string]any{
		"flag absent": {"new_password": "a-brand-new-password"},
		"flag false":  {"new_password": "a-brand-new-password", "revoke_api_keys": false},
	} {
		code, b := h.resetPassword(t, admin, h.staffID, body)
		require.Equal(t, http.StatusNoContent, code, "%s: %s", name, b)
		require.Equal(t, before, h.keyCount(t, h.staffID), name)
	}
	// And no key entry: nothing was revoked.
	entries, err := h.auditStore.ListByEntity(context.Background(), "user", h.staffID, 20, 0)
	require.NoError(t, err)
	for _, e := range entries {
		require.NotEqual(t, "api_keys_revoked", e.Action)
	}
}

func TestAdminPasswordReset_CanRevokeEveryKeyAndSaysHowMany(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	h.setMaxYears(t, -1)
	staffKey := h.mintMine(t, h.session(t, asStaff), "tickets:read")
	adminKey := h.mintMine(t, h.session(t, asAdmin), "tickets:read")
	require.Equal(t, 2, h.keyCount(t, h.staffID))

	code, b := h.resetPassword(t, h.session(t, asAdmin), h.staffID,
		map[string]any{"new_password": "a-brand-new-password", "revoke_api_keys": true})
	require.Equal(t, http.StatusNoContent, code, "body: %s", b)

	require.Zero(t, h.keyCount(t, h.staffID))
	r := requestWithKey(t, h, staffKey.Token, "/api/v1/tickets")
	r.Body.Close()
	require.Equal(t, http.StatusUnauthorized, r.StatusCode, "the key stops working")
	r = requestWithKey(t, h, h.apiKey, "/api/v1/tickets")
	r.Body.Close()
	require.Equal(t, http.StatusUnauthorized, r.StatusCode, "the one that existed before too")

	require.Equal(t, 2, h.keyCount(t, h.adminID), "someone else's keys are untouched")
	_ = adminKey

	entries, err := h.auditStore.ListByEntity(context.Background(), "user", h.staffID, 20, 0)
	require.NoError(t, err)
	var found bool
	for _, e := range entries {
		if e.Action != "api_keys_revoked" {
			continue
		}
		found = true
		require.NotNil(t, e.ActorID)
		require.Equal(t, h.adminID, *e.ActorID)
		require.EqualValues(t, 2, e.After["keys_revoked"], "how many")
	}
	require.True(t, found, "revoking keys is audited, on the person's own record")
}

// A refused reset must not have taken the keys with it.
func TestAdminPasswordReset_AWeakPasswordRevokesNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, _ := h.resetPassword(t, h.session(t, asAdmin), h.staffID,
		map[string]any{"new_password": "x", "revoke_api_keys": true})
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, 1, h.keyCount(t, h.staffID))
}

// Nobody resets their own password to get rid of their keys, and the console
// commands are about getting back in, not about credentials.
func TestOwnPasswordChange_NeverTouchesKeys(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := loggedIn(t, h)
	res, b := s.send(t, http.MethodPatch, "/api/v1/me/password", map[string]any{
		"current_password": "password", "new_password": "a-brand-new-password", "revoke_api_keys": true,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", b)
	require.Equal(t, 1, h.keyCount(t, h.staffID), "the flag means nothing on a person's own change")
}
