package user_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The checks a handler runs before it writes anything (#419) are the same ones
// the write applies, so they cannot drift apart.
func TestRoleValid(t *testing.T) {
	for _, r := range []user.Role{user.RoleAdmin, user.RoleStaff, user.RoleUser} {
		require.True(t, r.Valid(), r)
	}
	for _, r := range []user.Role{"", "root", "ADMIN"} {
		require.False(t, r.Valid(), r)
	}
}

func TestValidateDisplayName(t *testing.T) {
	got, err := user.ValidateDisplayName("  Ann  ")
	require.NoError(t, err)
	require.Equal(t, "Ann", got)

	_, err = user.ValidateDisplayName("   ")
	require.True(t, errors.Is(err, user.ErrValidation))
}

func TestCheckNewPassword(t *testing.T) {
	cases := []struct {
		name, plain string
		ok          bool
	}{
		{"blank", "   ", false},
		{"too short", "b", false},
		{"too long", strings.Repeat("a", user.MaxPasswordLength+1), false},
		{"fine", "a-brand-new-password", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := user.CheckNewPassword(tc.plain)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.True(t, errors.Is(err, user.ErrValidation), "got %v", err)
		})
	}
}
