package admin_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// The SLA alert settings (#670) are auth-neutral: they decide what staff are
// told, not who may sign in. Like their siblings (sla_enabled, the three
// staff_email_*_override keys) they are known to PATCH and absent from
// AuthCriticalKeys, so an API key may change them.
func TestSLASettings_KnownAndAuthNeutral(t *testing.T) {
	require.Equal(t, "staff_email_sla_override", admin.KeyStaffEmailSLAOverride)
	for _, key := range []string{admin.KeySLAAlertsEnabled, admin.KeyStaffEmailSLAOverride} {
		require.True(t, admin.IsKnownKey(key), key)
		require.False(t, slices.Contains(admin.AuthCriticalKeys(), key), key)
	}
	for _, key := range []string{admin.KeyStaffEmailAssignedOverride, admin.KeyStaffEmailActivityOverride, admin.KeyStaffEmailGroupOverride} {
		require.False(t, slices.Contains(admin.AuthCriticalKeys(), key), "sibling %s", key)
	}
}

// The fourth staff-email override reads like the other three: unset,
// unrecognised or unreadable is "user", never a forced value.
func TestStaffEmailSLAOverride(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte // nil: no row
		want notification.OverrideMode
	}{
		{"no row defers to the user", nil, notification.OverrideUser},
		{"user", []byte(`"user"`), notification.OverrideUser},
		{"on", []byte(`"on"`), notification.OverrideOn},
		{"off", []byte(`"off"`), notification.OverrideOff},
		{"uppercase is not a mode", []byte(`"ON"`), notification.OverrideUser},
		{"unknown is not a mode", []byte(`"always"`), notification.OverrideUser},
		{"not a string is not a mode", []byte(`42`), notification.OverrideUser},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeAdminStore()
			if tc.raw != nil {
				store.data[admin.KeyStaffEmailSLAOverride] = tc.raw
			}
			got := admin.NewService(store).StaffEmailSLAOverride(context.Background())
			require.Equal(t, tc.want, got)
		})
	}
}
