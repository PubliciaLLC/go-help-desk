package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// sla_enabled on GET /me tells the ticket list whether to offer the SLA filter
// and column. Staff and administrators get it, always present so a client can
// tell false from an old server. A requester is never shown anything about SLA
// (their sla is null), so their /me has no such key. It is not on the public
// /site: whether an instance tracks SLA is not something an unauthenticated
// visitor needs.
func TestMe_SLAEnabled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	read := func(do func(*testing.T, string, string, any) *http.Response) (present, value bool) {
		t.Helper()
		res := do(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		var body map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
		raw, ok := body["sla_enabled"]
		if ok {
			require.NoError(t, json.Unmarshal(raw, &value))
		}
		return ok, value
	}
	callers := map[string]func(*testing.T, string, string, any) *http.Response{
		"staff": func(t *testing.T, m, p string, b any) *http.Response { return h.do(t, m, p, b) },
		"admin": h.doAsAdmin,
	}

	for name, do := range callers {
		present, value := read(do)
		require.True(t, present, "%s sees it", name)
		require.False(t, value, "%s: tracking is off by default", name)
	}

	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySLAEnabled, true))
	for name, do := range callers {
		present, value := read(do)
		require.True(t, present, name)
		require.True(t, value, "%s: follows the setting", name)
	}

	present, _ := read(h.doAsUser)
	require.False(t, present, "a requester is not told")

	site := h.doUnauth(t, http.MethodGet, "/api/v1/site", nil)
	var cfg map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(site.Body).Decode(&cfg))
	require.NotContains(t, cfg, "sla_enabled", "the public site config does not carry it")
}

// The key is absent, not false, when the setting cannot be read: a client reads
// false as "tracking is off" and would hide the SLA controls over a transient
// error.
func TestMe_SLAEnabled_UnreadableSettingLeavesTheKeyOut(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// A value that is JSON but not a bool, so reading it as one fails.
	_, err := h.tx.ExecContext(context.Background(),
		`INSERT INTO settings (key, value) VALUES ($1, '"maybe"') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		admin.KeySLAEnabled)
	require.NoError(t, err)

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var body map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.NotContains(t, body, "sla_enabled")
	require.Contains(t, body, "can_create_api_keys", "the rest of /me is still answered")
}

// An OAuth client's /me is the fixed machine/role/scopes answer and never
// reaches the account view, so it has no such key.
func TestMe_SLAEnabled_NotOnAnOAuthClient(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySLAEnabled, true))

	resp := h.createCredential(t, "/api/v1/admin/oauth-clients", map[string]any{
		"name": "integration", "scopes": []string{"tickets:read"},
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	token := h.oauthToken(t, created.ClientID, created.ClientSecret)

	res := h.doWithBearer(t, token, "/api/v1/me")
	require.Equal(t, http.StatusOK, res.StatusCode)
	var body map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Contains(t, body, "machine")
	require.NotContains(t, body, "sla_enabled")
}

// PATCH /me/email-preferences answers with the same account view as GET /me.
func TestMe_SLAEnabled_OnTheEmailPreferencesAnswer(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySLAEnabled, true))
	s := loggedIn(t, h)

	res, body := s.send(t, http.MethodPatch, "/api/v1/me/email-preferences", map[string]any{"email_notify_group": false})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, "true", string(got["sla_enabled"]))
}
