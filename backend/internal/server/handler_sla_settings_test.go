package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// snapshotSettings is every stored setting, for asserting that a refused PATCH
// wrote nothing. Compared before and after rather than by absence: migration
// 000067 seeds sla_alerts_enabled, so the key is not absent to begin with.
func snapshotSettings(t *testing.T, h *harness) map[string]json.RawMessage {
	t.Helper()
	all, err := h.adminSvc.ListAll(context.Background())
	require.NoError(t, err)
	out := make(map[string]json.RawMessage, len(all))
	for k, v := range all {
		out[k] = v
	}
	return out
}

// sla_alerts_enabled is a bool and nothing else (#670). A string "yes" or a
// null would be stored and read back as off, so the administrator would
// believe SLA alerts were on. Refused whole, and nothing is written.
func TestSLAAlertsEnabled_RefusesANonBool(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"string", "true"},
		{"string off", "false"},
		{"number", 1},
		{"null", nil},
		{"object", map[string]any{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := adminSession(t, h)

			before := snapshotSettings(t, h)
			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{admin.KeySLAAlertsEnabled: c.value})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
			require.Equal(t, before, snapshotSettings(t, h), "nothing was written")
		})
	}
}

// staff_email_sla_override takes user | on | off, and the refusal is the
// setting's own code, whatever was wrong with the value (#420). A refused
// value writes nothing, not even a valid key sent in the same body.
func TestStaffEmailSLAOverride_RefusesAnythingElse(t *testing.T) {
	cases := []struct {
		name  string
		value any
		msg   string
	}{
		{"unknown", "always", "a staff email override must be one of: user, on, off"},
		{"empty", "", "a staff email override must be one of: user, on, off"},
		{"uppercase", "ON", "a staff email override must be one of: user, on, off"},
		{"null", nil, "a staff email override must be one of: user, on, off"},
		{"bool", true, "a staff email override must be a string"},
		{"number", 1, "a staff email override must be a string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := adminSession(t, h)

			before := snapshotSettings(t, h)
			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
				admin.KeyStaffEmailSLAOverride: c.value,
				admin.KeySLAAlertsEnabled:      false,
			})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
			require.Equal(t, "invalid_staff_email_override", errCode(t, body))
			require.Contains(t, string(body), c.msg)
			require.Equal(t, before, snapshotSettings(t, h), "nothing was written, the valid key included")
		})
	}
}

// Both keys are auth-neutral, so an API key may set them, as it may set their
// siblings. The machine credential gets the same answer as a session.
func TestSLASettings_AMachineCredentialMayChangeThem(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		admin.KeySLAAlertsEnabled:           true,
		admin.KeyStaffEmailSLAOverride:      "on",
		admin.KeySLAEnabled:                 true,
		admin.KeyStaffEmailAssignedOverride: "off",
	})
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	on, err := h.adminSvc.SLAAlertsEnabled(ctx)
	require.NoError(t, err)
	require.True(t, on)
	require.Equal(t, notification.OverrideOn, h.adminSvc.StaffEmailSLAOverride(ctx))
}

// The fourth override is read through the same choice table as the other
// three, so the settings dump reports the value in force for it too: a damaged
// stored value is reported as what the reader enforces, "user".
func TestStaffEmailSLAOverride_DumpReportsTheValueInForce(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	sess := adminSession(t, h)

	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyStaffEmailSLAOverride, []byte(`"bogus"`)))
	require.JSONEq(t, `"user"`, string(settingsDump(t, sess)[admin.KeyStaffEmailSLAOverride]))

	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyStaffEmailSLAOverride, []byte(`"off"`)))
	require.JSONEq(t, `"off"`, string(settingsDump(t, sess)[admin.KeyStaffEmailSLAOverride]))
}
