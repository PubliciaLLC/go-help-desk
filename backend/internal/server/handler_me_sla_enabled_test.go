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
