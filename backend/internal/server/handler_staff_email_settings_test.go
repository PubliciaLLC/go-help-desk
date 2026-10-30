package server_test

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

var staffOverrideKeys = []string{
	admin.KeyStaffEmailAssignedOverride,
	admin.KeyStaffEmailActivityOverride,
	admin.KeyStaffEmailGroupOverride,
}

// What the reader reports when nothing is overridden.
var leftToUsers = notification.StaffOverrides{
	Assigned: notification.OverrideUser, Activity: notification.OverrideUser, Group: notification.OverrideUser,
}

// The admin override for each staff-email switch (#441): user | on | off.
// Unset is "user", every switch left to the person.
func TestStaffEmailOverride_DefaultsToTheUsersChoice(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.Equal(t, leftToUsers, h.adminSvc.StaffEmailOverrides(context.Background()))
}

func TestStaffEmailOverride_AcceptsTheThreeModes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		admin.KeyStaffEmailAssignedOverride: "off",
		admin.KeyStaffEmailActivityOverride: "on",
		admin.KeyStaffEmailGroupOverride:    "user",
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	require.Equal(t, notification.StaffOverrides{
		Assigned: notification.OverrideOff,
		Activity: notification.OverrideOn,
		Group:    notification.OverrideUser,
	}, h.adminSvc.StaffEmailOverrides(context.Background()))
}

func TestStaffEmailOverride_RefusesAnythingElse(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	for _, key := range staffOverrideKeys {
		for name, v := range map[string]any{"unknown": "always", "empty": "", "uppercase": "ON", "null": nil, "bool": true, "number": 1} {
			t.Run(key+" "+name, func(t *testing.T) {
				res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{key: v})
				require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
			})
		}
	}
	// Nothing was stored by the refusals.
	require.Equal(t, leftToUsers, h.adminSvc.StaffEmailOverrides(context.Background()))
}

// A stored value the reader does not recognise is not a decision: the person's
// own switch applies. (Only a direct database edit can put one there.)
func TestStaffEmailOverride_ADamagedValueDefersToTheUser(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	for _, key := range staffOverrideKeys {
		require.NoError(t, h.adminSvc.SetRaw(ctx, key, []byte(`"bogus"`)))
	}
	require.Equal(t, leftToUsers, h.adminSvc.StaffEmailOverrides(ctx))
}

// Only an administrator may force a switch for everyone.
func TestStaffEmailOverride_StaffCannotChangeIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.do(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{admin.KeyStaffEmailAssignedOverride: "off"})
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, leftToUsers, h.adminSvc.StaffEmailOverrides(context.Background()))
}

// After migration 000052 on an upgraded install, Settings -> Email has what it
// needs to show the three overrides as Off, and an administrator can turn them on.
func TestStaffEmailOverride_AfterUpgradeMigrationShowsOffAndCanBeSwitchedOn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	sqlBytes, err := os.ReadFile("../database/migrations/000052_staff_email_off_on_upgrade.up.sql")
	require.NoError(t, err)
	_, err = h.tx.Exec(`DELETE FROM settings WHERE key LIKE 'staff_email_%_override'`)
	require.NoError(t, err)
	_, err = h.tx.Exec(string(sqlBytes))
	require.NoError(t, err)

	got := settingsDump(t, sess)
	for _, key := range staffOverrideKeys {
		require.Contains(t, got, key)
		require.JSONEq(t, `"off"`, string(got[key]), key)
	}

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		admin.KeyStaffEmailAssignedOverride: "on",
		admin.KeyStaffEmailActivityOverride: "user",
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.Equal(t, notification.StaffOverrides{
		Assigned: notification.OverrideOn,
		Activity: notification.OverrideUser,
		Group:    notification.OverrideOff,
	}, h.adminSvc.StaffEmailOverrides(context.Background()))
}
