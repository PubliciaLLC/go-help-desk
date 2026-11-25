package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// "Require SAML for staff and users" (saml_enabled) used to be enforced on
// password sign-in only. With an OIDC provider configured, staff and ordinary
// users could still sign in through it, which is the opposite of what the
// setting says. Administrators keep their exemption, as for passwords.
func TestOIDCCallback_RequireSAML(t *testing.T) {
	cases := []struct {
		name        string
		samlEnabled bool
		role        user.Role // role of the existing federated account; "" means a first sign-in
		wantRefused bool
	}{
		{"staff refused", true, user.RoleStaff, true},
		{"user refused", true, user.RoleUser, true},
		{"admin keeps access", true, user.RoleAdmin, false},
		{"first sign-in refused", true, "", true},
		{"staff unaffected when off", false, user.RoleStaff, false},
		{"user unaffected when off", false, user.RoleUser, false},
		{"first sign-in unaffected when off", false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oh, cleanup := newOIDCHarness(t)
			defer cleanup()
			ctx := context.Background()

			const email = "sso-person@test.local"
			if tc.role != "" {
				_, err := oh.userSvc.Create(ctx, user.CreateUserInput{
					Email:       email,
					DisplayName: "SSO Person",
					Role:        tc.role,
					OIDCSubject: "sso-sub",
				})
				require.NoError(t, err)
			}
			require.NoError(t, oh.adminSvc.SetSAMLEnabled(ctx, tc.samlEnabled))

			resp := oh.login(t, fakeOIDCClaims{
				Subject:       "sso-sub",
				Email:         email,
				EmailVerified: true,
				Name:          "SSO Person",
			})

			if tc.wantRefused {
				requireLoginRefused(t, oh.harness, resp, "saml_required")
			} else {
				require.Equal(t, http.StatusSeeOther, resp.StatusCode)
				require.Equal(t, "/", resp.Header.Get("Location"))
				_, authed := whoami(t, oh.harness, resp.Cookies())
				require.True(t, authed)
			}

			// A refusal must leave nothing behind: a first sign-in is not
			// provisioned into an account that could never be used.
			if tc.role == "" {
				_, exists := findUser(t, oh.harness, email)
				require.Equal(t, !tc.wantRefused, exists)
			}
		})
	}
}

// An existing local account with the address the provider sends would be
// adopted by a first OIDC sign-in. Under Require SAML the sign-in is refused,
// so nothing is bound to the account.
func TestOIDCCallback_RequireSAML_DoesNotAdoptALocalAccount(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, oh.adminSvc.SetSAMLEnabled(ctx, true))

	resp := oh.login(t, fakeOIDCClaims{
		Subject:       "adopter-sub",
		Email:         "staff@test.local",
		EmailVerified: true,
		Name:          "Staff",
	})
	requireLoginRefused(t, oh.harness, resp, "saml_required")

	u, err := oh.userSvc.GetByIDAdmin(ctx, oh.staffID)
	require.NoError(t, err)
	require.Empty(t, u.OIDCSubject)
}
