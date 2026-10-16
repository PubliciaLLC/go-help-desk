package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
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

// createCredential makes a credential the way a person does, in a signed-in
// session: a key can create neither an API key nor an OAuth client.
func (h *harness) createCredential(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	return h.doSession(t, h.session(t, asAdmin), http.MethodPost, path, body)
}

// "API keys cannot create oauth. Keys cannot create keys." (CTO, 2026-10-16.)
// An OAuth client is a credential too: one made by a leaked key would outlive
// the key and could be used after the key is revoked.
func TestAdminCreateOAuthClient_NoMachineCredentialCanCreateOne(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	client := h.newServiceClient(t, "existing", allScopes()...)
	before, err := h.authStore.ListOAuthClients(context.Background())
	require.NoError(t, err)

	body := map[string]any{"name": "successor", "scopes": []string{"tickets:read"}}
	for name, c := range map[string]struct {
		do   func() *http.Response
		code string
	}{
		"an administrator's API key": {func() *http.Response {
			return h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/oauth-clients", body)
		}, "session_required"},
		"an OAuth client": {func() *http.Response {
			return h.doBearer(t, client.Token, http.MethodPost, "/api/v1/admin/oauth-clients", body)
		}, "forbidden"},
	} {
		t.Run(name, func(t *testing.T) {
			res := c.do()
			out := readAll(t, res)
			require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", out)
			require.Equal(t, c.code, errCode(t, out))
		})
	}
	after, err := h.authStore.ListOAuthClients(context.Background())
	require.NoError(t, err)
	require.Len(t, after, len(before), "nothing was created")

	t.Run("a signed-in administrator still can", func(t *testing.T) {
		res := h.createCredential(t, "/api/v1/admin/oauth-clients", body)
		out := readAll(t, res)
		require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", out)
	})
}

// Keys may still look at and remove OAuth clients.
func TestAdminOAuthClients_AKeyCanStillListAndRevoke(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	client := h.newServiceClient(t, "doomed", allScopes()...)

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/oauth-clients", nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	res = h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/oauth-clients/"+client.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

// A key can revoke itself on the admin route (and only itself, #466). Once it
// has, it is no credential at all.
func TestAdminAPIKeys_AKeyCanRevokeItself(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	self, err := h.authStore.GetByHash(context.Background(), auth.HashToken(h.adminKey))
	require.NoError(t, err)

	res := h.doAsAdmin(t, http.MethodDelete, adminKeys+"/"+self.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	res = h.doAsAdmin(t, http.MethodGet, adminKeys, nil)
	res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode, "the revoked key is no longer a credential")
}
