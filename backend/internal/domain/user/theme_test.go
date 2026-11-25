package user_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The three appearance choices (#709) and nothing else.
func TestTheme_Valid(t *testing.T) {
	cases := []struct {
		in   user.Theme
		want bool
	}{
		{user.ThemeLight, true},
		{user.ThemeDark, true},
		{user.ThemeDevice, true},
		{"light", true},
		{"", false},
		{"Dark", false},
		{"system", false},
		{"auto", false},
		{" dark", false},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			require.Equal(t, tc.want, tc.in.Valid())
		})
	}
}

func TestService_SetTheme(t *testing.T) {
	ctx := context.Background()
	store := newFakeUserStore()
	svc := user.NewService(store, user.WithBcryptCost(4))
	id := uuid.New()
	store.seed(user.User{ID: id, Email: "s@example.com", DisplayName: "S", Role: user.RoleStaff, Theme: user.ThemeDevice})

	require.NoError(t, svc.SetTheme(ctx, id, user.ThemeDark))
	got, err := svc.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, user.ThemeDark, got.Theme)
	require.Equal(t, 1, store.themeSets)

	// A value that is not one of the three is refused before the store is asked.
	for _, bad := range []user.Theme{"", "auto", "Dark", "system"} {
		err := svc.SetTheme(ctx, id, bad)
		require.True(t, errors.Is(err, user.ErrInvalidTheme), "%q: %v", bad, err)
	}
	require.Equal(t, 1, store.themeSets, "a refused theme reached the store")
	got, err = svc.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, user.ThemeDark, got.Theme)
}
