package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// "Staff may create their own API keys" (#450). The base refuses a setting it
// does not list with 400 unknown_setting, which is how an earlier setting
// shipped unusable; this is the end-to-end check that the toggle saves.
func TestStaffCanCreateAPIKeys_SettingRoundTrip(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.False(t, h.adminSvc.StaffCanCreateAPIKeys(ctx), "off until an administrator turns it on")

	sess := signInAsAdmin(t, h)
	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"staff_can_create_api_keys": true})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.True(t, h.adminSvc.StaffCanCreateAPIKeys(ctx))

	res, body = sess.send(t, http.MethodGet, "/api/v1/admin/settings", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var dump map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &dump))
	require.JSONEq(t, "true", string(dump["staff_can_create_api_keys"]))
}

// Auth-critical, so session-only. Widening who may mint a long-lived bearer
// credential is a permission change of the same kind as closed_reopen_policy:
// a leaked administrator key must not be able to switch it on and then use it.
func TestStaffCanCreateAPIKeys_RefusedToMachineCredentials(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"staff_can_create_api_keys": true})
	defer res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	var got struct{ Error struct{ Code string } }
	require.NoError(t, json.NewDecoder(res.Body).Decode(&got))
	require.Equal(t, "session_required", got.Error.Code)
	require.False(t, h.adminSvc.StaffCanCreateAPIKeys(context.Background()))
}

func TestStaffCanCreateAPIKeys_MustBeABoolean(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := signInAsAdmin(t, h)

	for name, v := range map[string]any{"string": "true", "null": nil, "number": 1} {
		t.Run(name, func(t *testing.T) {
			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{"staff_can_create_api_keys": v})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
			// Refused for its value, not for being an unknown setting.
			require.NotContains(t, string(body), "unknown_setting")
			require.False(t, h.adminSvc.StaffCanCreateAPIKeys(context.Background()))
		})
	}
}

func TestStaffCanCreateAPIKeys_IsAuthCritical(t *testing.T) {
	require.Contains(t, admin.AuthCriticalKeys(), admin.KeyStaffCanCreateAPIKeys)
	require.Contains(t, admin.KnownKeys(), admin.KeyStaffCanCreateAPIKeys)
}
