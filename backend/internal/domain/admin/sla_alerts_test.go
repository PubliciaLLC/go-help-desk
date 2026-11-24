package admin_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// sla_alerts_enabled gates both SLA events (#670). Like SLAEnabled, a failed
// read is returned, not swallowed: the sweep must skip its tick rather than
// stamp silently because a read failed.
func TestSLAAlertsEnabled(t *testing.T) {
	require.Equal(t, "sla_alerts_enabled", admin.KeySLAAlertsEnabled)
	require.True(t, admin.IsKnownKey(admin.KeySLAAlertsEnabled))

	cases := []struct {
		name    string
		raw     []byte // nil: no row
		want    bool
		wantErr bool
	}{
		{"on", []byte("true"), true, false},
		{"off", []byte("false"), false, false},
		{"no row is a failed read, not off", nil, false, true},
		{"not a bool is a failed read", []byte(`"yes"`), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeAdminStore()
			if tc.raw != nil {
				store.data[admin.KeySLAAlertsEnabled] = tc.raw
			}
			got, err := admin.NewService(store).SLAAlertsEnabled(context.Background())
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, got)
		})
	}
}
