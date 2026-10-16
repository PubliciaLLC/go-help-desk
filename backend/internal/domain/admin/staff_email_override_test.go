package admin_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// The administrator's override of each staff-email switch (#441). The reader
// never errors and never returns a forced mode it was not given: whatever it
// cannot read is "user", the person's own choice.
func TestStaffEmailOverrides(t *testing.T) {
	ctx := context.Background()
	user := notification.OverrideUser

	t.Run("unset is the person's own choice for all three", func(t *testing.T) {
		svc := admin.NewService(newFakeAdminStore())
		require.Equal(t, notification.StaffOverrides{Assigned: user, Activity: user, Group: user}, svc.StaffEmailOverrides(ctx))
	})

	t.Run("each key is read for its own switch", func(t *testing.T) {
		svc := admin.NewService(newFakeAdminStore())
		require.NoError(t, svc.SetString(ctx, admin.KeyStaffEmailAssignedOverride, "on"))
		require.NoError(t, svc.SetString(ctx, admin.KeyStaffEmailActivityOverride, "off"))
		require.Equal(t, notification.StaffOverrides{
			Assigned: notification.OverrideOn, Activity: notification.OverrideOff, Group: user,
		}, svc.StaffEmailOverrides(ctx))
	})

	t.Run("an unrecognised or unreadable value is not a forced one", func(t *testing.T) {
		svc := admin.NewService(newFakeAdminStore())
		require.NoError(t, svc.SetRaw(ctx, admin.KeyStaffEmailAssignedOverride, []byte(`"ON"`)))
		require.NoError(t, svc.SetRaw(ctx, admin.KeyStaffEmailActivityOverride, []byte(`true`)))
		require.NoError(t, svc.SetRaw(ctx, admin.KeyStaffEmailGroupOverride, []byte(`null`)))
		require.Equal(t, notification.StaffOverrides{Assigned: user, Activity: user, Group: user}, svc.StaffEmailOverrides(ctx))
	})
}
