package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Turning ticket scope on tells the administrator which OAuth clients will go
// quiet (#420, decision Q8): a sentence on the save, and a banner that stays
// until every client has a group or is switched off.

func patchScope(t *testing.T, h *harness, on bool) (int, map[string]any) {
	t.Helper()
	resp := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"ticket_scope_enforced": on})
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var out map[string]any
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &out))
	}
	return resp.StatusCode, out
}

func securityBanner(t *testing.T, h *harness) []string {
	t.Helper()
	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Clients []string `json:"oauth_clients_without_groups"`
	}
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &body))
	require.NotNil(t, body.Clients, "an array, never null: %s", raw)
	return body.Clients
}

func TestScopeEnforcement_SaveWarnsWhichClientsWillSeeNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	nagios := h.newServiceClient(t, "Nagios", "tickets:read")
	zapier := h.newServiceClient(t, "Zapier", "tickets:read")
	covered := h.newServiceClient(t, "Covered", "tickets:read")
	off := h.newServiceClient(t, "Switched off", "tickets:read")

	g, err := h.groupSvc.Create(ctx, "Monitoring", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, covered.UserID))
	require.NoError(t, h.userSvc.Disable(ctx, off.UserID))

	status, body := patchScope(t, h, true)
	require.Equal(t, http.StatusOK, status, "a warning is a 200 with a body, not a refusal")
	warning, _ := body["warning"].(string)
	require.Contains(t, warning, "Nagios")
	require.Contains(t, warning, "Zapier")
	require.NotContains(t, warning, "Covered", "it has a group")
	require.NotContains(t, warning, "Switched off", "it is switched off, so it acts as nobody")
	require.True(t, h.adminSvc.TicketScopeEnforced(ctx), "it warns and does not refuse")

	// Turning it on again repeats nothing.
	status, _ = patchScope(t, h, true)
	require.Equal(t, http.StatusNoContent, status)

	// Nor does turning it off.
	status, _ = patchScope(t, h, false)
	require.Equal(t, http.StatusNoContent, status)

	// Once every client has a group (or is off) turning it on is silent.
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, zapier.UserID))
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, nagios.UserID))
	status, _ = patchScope(t, h, true)
	require.Equal(t, http.StatusNoContent, status)
}

func TestScopeEnforcement_BannerStaysUntilEveryClientHasAGroupOrIsOff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	require.Empty(t, securityBanner(t, h), "scope is not enforced, so no client is affected")

	enableScope(t, h)
	require.Equal(t, []string{"Nagios"}, securityBanner(t, h))

	// Cleared by giving the client's user a group...
	g, err := h.groupSvc.Create(ctx, "Monitoring", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, c.UserID))
	require.Empty(t, securityBanner(t, h))

	// ...or by switching it off.
	require.NoError(t, h.groupSvc.RemoveMember(ctx, g.ID, c.UserID))
	require.Equal(t, []string{"Nagios"}, securityBanner(t, h))
	require.NoError(t, h.userSvc.Disable(ctx, c.UserID))
	require.Empty(t, securityBanner(t, h))
}

// The sentence joins the SSO guard's warning rather than replacing it, since
// both can apply to one save.
func TestScopeEnforcement_WarningJoinsTheSSOWarning(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	createOIDCAdmin(t, oh, "partial-oidc-admin-joined@test.local", "partial-oidc-admin-joined-sub")
	oh.harness.newServiceClient(t, "Nagios", "tickets:read")

	sess := &session{h: oh.harness}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"oidc_enabled": false, "ticket_scope_enforced": true})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "partial-oidc-admin-joined@test.local", "the SSO warning is still there")
	require.Contains(t, string(body), "Nagios", "and so is the scope one")
}
