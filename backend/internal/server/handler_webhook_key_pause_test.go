package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// A webhook a key made, or last re-pointed, stops firing when that key is
// revoked or expires (CTO, 2026-10-16). A key may still create and edit
// webhooks, but a destination chosen by a credential that is gone must not
// keep receiving ticket data. Judged at delivery time from the key's own row,
// so expiry needs no sweeper. Only a signed-in administrator re-approves one.

const hooksPath = "/api/v1/admin/webhooks"

func (h *harness) adminKeyID(t *testing.T) uuid.UUID {
	t.Helper()
	k, err := h.authStore.GetByHash(context.Background(), auth.HashToken(h.adminKey))
	require.NoError(t, err)
	return k.ID
}

// mintAdminKey makes a second administrator key, so a test can revoke or expire
// the key a hook is tied to without losing the harness's own.
func (h *harness) mintAdminKey(t *testing.T, scopes ...string) (raw string, id uuid.UUID) {
	t.Helper()
	k := h.mintMine(t, h.session(t, asAdmin), scopes...)
	return k.Token, k.ID
}

func keyDo(t *testing.T, h *harness, raw, method, path string, body any) *http.Response {
	t.Helper()
	return withKey(t, h, raw, method, path, body)
}

func createHookWith(t *testing.T, h *harness, do func(method, path string, body any) *http.Response) uuid.UUID {
	t.Helper()
	res := do(http.MethodPost, hooksPath, map[string]any{"url": "https://example.com/hook", "events": []string{"*"}})
	b := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
	var out struct{ ID uuid.UUID }
	require.NoError(t, json.Unmarshal(b, &out))
	return out.ID
}

func (h *harness) hook(t *testing.T, id uuid.UUID) authstore.WebhookConfig {
	t.Helper()
	wh, err := h.authStore.GetWebhook(context.Background(), id)
	require.NoError(t, err)
	return wh
}

func (h *harness) firing(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	hooks, err := h.authStore.ListEnabledWebhooks(context.Background())
	require.NoError(t, err)
	for _, wh := range hooks {
		if wh.ID == id {
			return !wh.Paused
		}
	}
	return false
}

func TestWebhookKeyPause_ARevokedKeysWebhookStopsFiring(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	raw, keyID := h.mintAdminKey(t, "webhooks:write", "webhooks:read")
	id := createHookWith(t, h, func(m, p string, b any) *http.Response { return keyDo(t, h, raw, m, p, b) })

	require.NotNil(t, h.hook(t, id).APIKeyID)
	require.Equal(t, keyID, *h.hook(t, id).APIKeyID, "tied to the key that made it")
	require.True(t, h.firing(t, id), "fires while its key is live")

	require.NoError(t, h.authStore.Delete(context.Background(), keyID))

	wh := h.hook(t, id)
	require.True(t, wh.Paused)
	require.Contains(t, wh.PausedReason, "revoked or expired API key")
	require.False(t, h.firing(t, id), "and stops when it is revoked")
}

func TestWebhookKeyPause_AnExpiredKeysWebhookStopsFiring(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	raw, keyID := h.mintAdminKey(t, "webhooks:write")
	id := createHookWith(t, h, func(m, p string, b any) *http.Response { return keyDo(t, h, raw, m, p, b) })
	require.True(t, h.firing(t, id))

	// Time passes: no job runs, the key's own expiry is all there is.
	_, err := h.tx.Exec(`UPDATE api_keys SET expires_at = clock_timestamp() - interval '1 minute' WHERE id = $1`, keyID)
	require.NoError(t, err)

	require.True(t, h.hook(t, id).Paused)
	require.False(t, h.firing(t, id), "an expired key is treated like a revoked one")
}

func TestWebhookKeyPause_AKeyRepointingASessionMadeWebhookTiesItToThatKey(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := createHookWith(t, h, func(m, p string, b any) *http.Response {
		return h.doSession(t, h.session(t, asAdmin), m, p, b)
	})
	require.Nil(t, h.hook(t, id).APIKeyID, "made in a session: no key")

	raw, keyID := h.mintAdminKey(t, "webhooks:write")

	t.Run("a change that is not the address leaves it alone", func(t *testing.T) {
		res := keyDo(t, h, raw, http.MethodPatch, hooksPath+"/"+id.String(), map[string]any{"events": []string{"ticket.created"}})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Nil(t, h.hook(t, id).APIKeyID)
	})
	t.Run("re-pointing it ties it", func(t *testing.T) {
		res := keyDo(t, h, raw, http.MethodPatch, hooksPath+"/"+id.String(), map[string]any{"url": "https://example.com/elsewhere"})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.NotNil(t, h.hook(t, id).APIKeyID)
		require.Equal(t, keyID, *h.hook(t, id).APIKeyID)
		require.NoError(t, h.authStore.Delete(context.Background(), keyID))
		require.False(t, h.firing(t, id), "and it dies with the key")
	})
}

func TestWebhookKeyPause_ASessionReapprovesAndItFiresAgain(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	raw, keyID := h.mintAdminKey(t, "webhooks:write")
	id := createHookWith(t, h, func(m, p string, b any) *http.Response { return keyDo(t, h, raw, m, p, b) })
	require.NoError(t, h.authStore.Delete(context.Background(), keyID))
	require.False(t, h.firing(t, id))

	res := h.doSession(t, h.session(t, asAdmin), http.MethodPost, hooksPath+"/"+id.String()+"/reapprove", nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	wh := h.hook(t, id)
	require.Nil(t, wh.APIKeyID, "re-approving clears the link to the key")
	require.False(t, wh.Paused)
	require.True(t, h.firing(t, id))
}

func TestWebhookKeyPause_ASessionEditingTheAddressTakesItOver(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	raw, keyID := h.mintAdminKey(t, "webhooks:write")
	id := createHookWith(t, h, func(m, p string, b any) *http.Response { return keyDo(t, h, raw, m, p, b) })

	res := h.doSession(t, h.session(t, asAdmin), http.MethodPatch, hooksPath+"/"+id.String(),
		map[string]any{"url": "https://example.com/chosen-by-a-person"})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Nil(t, h.hook(t, id).APIKeyID, "the last person to point it is a person")
	require.NoError(t, h.authStore.Delete(context.Background(), keyID))
	require.True(t, h.firing(t, id))
}

func TestWebhookKeyPause_AKeyCannotReapprove(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	raw, keyID := h.mintAdminKey(t, "webhooks:write")
	id := createHookWith(t, h, func(m, p string, b any) *http.Response { return keyDo(t, h, raw, m, p, b) })
	// A different live key tries; the paused hook's own key is gone.
	require.NoError(t, h.authStore.Delete(context.Background(), keyID))

	client := h.newServiceClient(t, "approver", allScopes()...)
	for name, res := range map[string]*http.Response{
		"an administrator's API key": h.doAsAdmin(t, http.MethodPost, hooksPath+"/"+id.String()+"/reapprove", nil),
		"an OAuth client":            h.doBearer(t, client.Token, http.MethodPost, hooksPath+"/"+id.String()+"/reapprove", nil),
	} {
		b := readAll(t, res)
		require.Equal(t, http.StatusForbidden, res.StatusCode, "%s: %s", name, b)
	}
	require.True(t, h.hook(t, id).Paused, "still paused")
}

func TestWebhookKeyPause_ASessionMadeWebhookIsUnaffectedByAnyKey(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := createHookWith(t, h, func(m, p string, b any) *http.Response {
		return h.doSession(t, h.session(t, asAdmin), m, p, b)
	})
	_, keyID := h.mintAdminKey(t, "webhooks:write")
	require.NoError(t, h.authStore.Delete(context.Background(), keyID))
	_, err := h.tx.Exec(`UPDATE api_keys SET expires_at = clock_timestamp() - interval '1 day'`)
	require.NoError(t, err)

	require.False(t, h.hook(t, id).Paused)
	require.True(t, h.firing(t, id))
}

func TestWebhookKeyPause_TheListSaysWhyItIsPaused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	raw, keyID := h.mintAdminKey(t, "webhooks:write")
	id := createHookWith(t, h, func(m, p string, b any) *http.Response { return keyDo(t, h, raw, m, p, b) })
	require.NoError(t, h.authStore.Delete(context.Background(), keyID))

	res := h.doAsAdmin(t, http.MethodGet, hooksPath, nil)
	var list []struct {
		ID           uuid.UUID `json:"id"`
		Paused       bool      `json:"paused"`
		PausedReason string    `json:"paused_reason"`
	}
	require.NoError(t, json.Unmarshal(readAll(t, res), &list))
	var found bool
	for _, e := range list {
		if e.ID == id {
			found = true
			require.True(t, e.Paused)
			require.Equal(t, "Created by a revoked or expired API key", e.PausedReason)
		}
	}
	require.True(t, found)
}
