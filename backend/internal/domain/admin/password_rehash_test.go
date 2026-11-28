package admin_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

func TestAdminService_PasswordRehashGraceDays(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		value any // nil: leave unset
		want  int
	}{
		{name: "unset is the default", value: nil, want: admin.PasswordRehashGraceDefault},
		{name: "stored value is used", value: 7, want: 7},
		{name: "the minimum is kept", value: admin.PasswordRehashGraceMin, want: admin.PasswordRehashGraceMin},
		{name: "the maximum is kept", value: admin.PasswordRehashGraceMax, want: admin.PasswordRehashGraceMax},
		// A value no handler would store falls back to the default rather
		// than to "refuse everyone now" (0) or "never" (huge).
		{name: "zero falls back", value: 0, want: admin.PasswordRehashGraceDefault},
		{name: "negative falls back", value: -3, want: admin.PasswordRehashGraceDefault},
		{name: "over the maximum falls back", value: admin.PasswordRehashGraceMax + 1, want: admin.PasswordRehashGraceDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := admin.NewService(newFakeAdminStore())
			if tc.value != nil {
				require.NoError(t, svc.SetInt(ctx, admin.KeyPasswordRehashGraceDays, tc.value.(int)))
			}
			require.Equal(t, tc.want, svc.PasswordRehashGraceDays(ctx))
		})
	}
	require.Equal(t, 30, admin.PasswordRehashGraceDefault)
}

func TestAdminService_PasswordRehashDeadline(t *testing.T) {
	ctx := context.Background()
	started := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	t.Run("no start recorded means no deadline", func(t *testing.T) {
		svc := admin.NewService(newFakeAdminStore())
		_, ok := svc.PasswordRehashDeadline(ctx)
		require.False(t, ok)
	})

	t.Run("an unreadable start means no deadline", func(t *testing.T) {
		svc := admin.NewService(newFakeAdminStore())
		require.NoError(t, svc.SetString(ctx, admin.KeyPasswordRehashStartedAt, "last tuesday"))
		_, ok := svc.PasswordRehashDeadline(ctx)
		require.False(t, ok)
	})

	t.Run("deadline is the start plus N days", func(t *testing.T) {
		svc := admin.NewService(newFakeAdminStore())
		require.NoError(t, svc.SetString(ctx, admin.KeyPasswordRehashStartedAt, started.Format(time.RFC3339)))
		got, ok := svc.PasswordRehashDeadline(ctx)
		require.True(t, ok)
		require.Equal(t, started.AddDate(0, 0, 30), got, "default is 30 days")

		require.NoError(t, svc.SetInt(ctx, admin.KeyPasswordRehashGraceDays, 10))
		got, ok = svc.PasswordRehashDeadline(ctx)
		require.True(t, ok)
		require.Equal(t, started.AddDate(0, 0, 10), got, "N is read each time, so an admin can move it")
	})
}

func TestPasswordRehashKeysAreKnown(t *testing.T) {
	require.True(t, admin.IsKnownKey(admin.KeyPasswordRehashGraceDays))
	require.True(t, admin.IsKnownKey(admin.KeyPasswordRehashStartedAt), "a migration seeds it")
}

// A leaked API key must not be able to push the deadline out or restart it.
func TestAuthCriticalKeys_IncludesPasswordRehashKeys(t *testing.T) {
	require.Contains(t, admin.AuthCriticalKeys(), admin.KeyPasswordRehashGraceDays)
	require.Contains(t, admin.AuthCriticalKeys(), admin.KeyPasswordRehashStartedAt)
}
