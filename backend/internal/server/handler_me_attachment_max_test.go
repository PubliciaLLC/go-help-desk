package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// #673: GET /me carries attachment_max_bytes, the instance-wide per-file limit
// in bytes, for every account role so the client can cap its file picker. It
// is absent on an OAuth client's /me, whose shape does not change (an API key's /me
// answers as its owner and carries it).
func TestMe_AttachmentMaxBytes(t *testing.T) {
	const mib = 1 << 20
	h, cleanup := newHarness(t)
	defer cleanup()

	read := func(do func(*testing.T, string, string, any) *http.Response) int64 {
		t.Helper()
		res := do(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		var body map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
		raw, ok := body["attachment_max_bytes"]
		require.True(t, ok, "attachment_max_bytes is on /me")
		var got int64
		require.NoError(t, json.Unmarshal(raw, &got))
		return got
	}
	callers := map[string]func(*testing.T, string, string, any) *http.Response{
		"admin": h.doAsAdmin,
		"staff": func(t *testing.T, m, p string, b any) *http.Response { return h.do(t, m, p, b) },
		"user":  h.doAsUser,
	}

	for name, do := range callers {
		require.Equal(t, int64(26214400), read(do), "%s: 25 MB by default", name)
	}

	setAttachmentMaxMB(t, h, 10)
	for name, do := range callers {
		require.Equal(t, int64(10*mib), read(do), "%s: follows attachment_max_mb", name)
	}
}

func TestMe_AttachmentMaxBytes_NotOnAnOAuthClient(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

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
	require.NotContains(t, body, "attachment_max_bytes")
}
