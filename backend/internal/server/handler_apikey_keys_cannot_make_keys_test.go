package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Keys can't make keys (CTO, 2026-10-16). Only a signed-in person creates an
// API key. Otherwise a key holding credentials:write mints a successor that
// expires up to two years out, and that one another, and the lifetime limit
// means nothing.
func TestAdminCreateAPIKey_NoMachineCredentialCanCreateAnyKey(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	client := h.newServiceClient(t, "minter", allScopes()...)

	body := map[string]any{"name": "successor", "scopes": []string{"tickets:read"}, "expires_at": inYears(1)}
	for name, c := range map[string]struct {
		do   func(map[string]any) *http.Response
		code string
	}{
		"an administrator's API key": {func(b map[string]any) *http.Response {
			return h.doAsAdmin(t, http.MethodPost, adminKeys, b)
		}, "session_required"},
		// An OAuth client is a staff-role identity, so the admin router's role
		// check refuses it before the route is reached: a different code, the
		// same refusal.
		"an OAuth client": {func(b map[string]any) *http.Response {
			return h.doBearer(t, client.Token, http.MethodPost, adminKeys, b)
		}, "forbidden"},
	} {
		do := c.do
		t.Run(name, func(t *testing.T) {
			for label, b := range map[string]map[string]any{
				"for itself":       body,
				"for the owner":    {"name": "x", "scopes": []string{"tickets:read"}, "expires_at": inYears(1), "user_id": h.adminID.String()},
				"for someone else": {"name": "x", "scopes": []string{"tickets:read"}, "expires_at": inYears(1), "user_id": h.staffID.String()},
				"with no expiry":   {"name": "x", "scopes": []string{"tickets:read"}},
			} {
				res := do(b)
				out := readAll(t, res)
				require.Equal(t, http.StatusForbidden, res.StatusCode, "%s: %s", label, out)
				// session_required is what /me/api-keys answers a machine credential with.
				require.Equal(t, c.code, errCode(t, out), label)
			}
		})
	}

	keys, err := h.authStore.ListByUser(context.Background(), h.adminID)
	require.NoError(t, err)
	require.Len(t, keys, 1, "only the harness's own key exists: nothing was minted")
}

// A key may still look at and revoke keys; it just cannot make one.
func TestAdminAPIKeys_AKeyCanStillListAndRevoke(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	victim := h.mintMine(t, h.session(t, asAdmin), "tickets:read")

	res := h.doAsAdmin(t, http.MethodGet, adminKeys, nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	// A key revokes only itself (#466), which is unchanged; the harness's own
	// admin key is the one making the call.
	res = h.doAsAdmin(t, http.MethodDelete, adminKeys+"/"+victim.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

// createCredential makes a credential the way a person does. API keys are made
// in a signed-in session, since a key cannot make one; an OAuth client is still
// made by the administrator's API key.
func (h *harness) createCredential(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	if path == adminKeys {
		return h.doSession(t, h.session(t, asAdmin), http.MethodPost, path, body)
	}
	return h.doAsAdmin(t, http.MethodPost, path, body)
}
