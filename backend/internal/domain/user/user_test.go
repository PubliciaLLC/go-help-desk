package user_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
)

func TestUser_Validate(t *testing.T) {
	base := user.User{
		ID:          uuid.New(),
		Email:       "alice@example.com",
		DisplayName: "Alice",
		Role:        user.RoleUser,
	}

	cases := []struct {
		name    string
		mutate  func(*user.User)
		wantErr bool
	}{
		{name: "valid", mutate: func(*user.User) {}, wantErr: false},
		{name: "empty email", mutate: func(u *user.User) { u.Email = "" }, wantErr: true},
		{name: "whitespace email", mutate: func(u *user.User) { u.Email = "   " }, wantErr: true},
		{name: "empty display name", mutate: func(u *user.User) { u.DisplayName = "" }, wantErr: true},
		{name: "whitespace display name", mutate: func(u *user.User) { u.DisplayName = "   " }, wantErr: true},
		{name: "invalid role", mutate: func(u *user.User) { u.Role = "superuser" }, wantErr: true},
		{name: "admin role", mutate: func(u *user.User) { u.Role = user.RoleAdmin }, wantErr: false},
		{name: "staff role", mutate: func(u *user.User) { u.Role = user.RoleStaff }, wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := base
			tc.mutate(&u)
			err := u.Validate()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestIsLocalAuthAllowed(t *testing.T) {
	cases := []struct {
		name        string
		role        user.Role
		samlEnabled bool
		want        bool
	}{
		{name: "saml off, admin", role: user.RoleAdmin, samlEnabled: false, want: true},
		{name: "saml off, staff", role: user.RoleStaff, samlEnabled: false, want: true},
		{name: "saml off, user", role: user.RoleUser, samlEnabled: false, want: true},
		{name: "saml on, admin", role: user.RoleAdmin, samlEnabled: true, want: true},
		{name: "saml on, staff", role: user.RoleStaff, samlEnabled: true, want: false},
		{name: "saml on, user", role: user.RoleUser, samlEnabled: true, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := user.User{Role: tc.role}
			require.Equal(t, tc.want, user.IsLocalAuthAllowed(u, tc.samlEnabled))
		})
	}
}
