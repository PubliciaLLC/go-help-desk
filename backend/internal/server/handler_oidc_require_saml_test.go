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

// createFederated makes an account already bound to an OIDC subject.
func createFederated(t *testing.T, oh *oidcHarness, email string, role user.Role, sub string) user.User {
	t.Helper()
	u, err := oh.userSvc.Create(context.Background(), user.CreateUserInput{
		Email: email, DisplayName: "Federated", Role: role, OIDCSubject: sub,
	})
	require.NoError(t, err)
	return u
}

// The administrator's way in is the whole point of the exemption: it must not
// depend on the address the provider sends today matching the stored one.
func TestOIDCCallback_RequireSAML_AdminPassesWithAChangedProviderAddress(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	ctx := context.Background()
	createFederated(t, oh, "boss@test.local", user.RoleAdmin, "boss-sub")
	require.NoError(t, oh.adminSvc.SetSAMLEnabled(ctx, true))

	resp := oh.login(t, fakeOIDCClaims{
		Subject: "boss-sub", Email: "boss.renamed@test.local", EmailVerified: true, Name: "Boss",
	})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	require.Equal(t, "/", resp.Header.Get("Location"))
	u, authed := whoami(t, oh.harness, resp.Cookies())
	require.True(t, authed)
	require.Equal(t, user.RoleAdmin, u.Role)
}

// Exempt from the requirement is not exempt from being disabled.
func TestOIDCCallback_RequireSAML_DisabledAdminIsStillDisabled(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	ctx := context.Background()
	a := createFederated(t, oh, "gone-boss@test.local", user.RoleAdmin, "gone-boss-sub")
	require.NoError(t, oh.userSvc.Disable(ctx, a.ID))
	require.NoError(t, oh.adminSvc.SetSAMLEnabled(ctx, true))

	resp := oh.login(t, fakeOIDCClaims{
		Subject: "gone-boss-sub", Email: "gone-boss@test.local", EmailVerified: true, Name: "Gone",
	})
	requireLoginRefused(t, oh.harness, resp, "account_disabled")
}

// An unknown subject arriving with an administrator's address is never adopted
// onto the administrator; under Require SAML it is refused the same way as any
// other unknown subject, and the administrator row stays unbound.
func TestOIDCCallback_RequireSAML_UnknownSubjectWithAnAdminsAddressIsRefused(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, oh.adminSvc.SetSAMLEnabled(ctx, true))

	resp := oh.login(t, fakeOIDCClaims{
		Subject: "impostor-sub", Email: "admin@test.local", EmailVerified: true, Name: "Impostor",
	})
	requireLoginRefused(t, oh.harness, resp, "saml_required")

	a, err := oh.userSvc.GetByIDAdmin(ctx, oh.adminID)
	require.NoError(t, err)
	require.Empty(t, a.OIDCSubject)
}

// The role is read at sign-in, not remembered: an administrator demoted to
// staff is refused on the next OIDC sign-in.
func TestOIDCCallback_RequireSAML_ADemotedAdminIsRefusedNextTime(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	ctx := context.Background()
	a := createFederated(t, oh, "demoted@test.local", user.RoleAdmin, "demoted-sub")
	require.NoError(t, oh.adminSvc.SetSAMLEnabled(ctx, true))
	claims := fakeOIDCClaims{Subject: "demoted-sub", Email: "demoted@test.local", EmailVerified: true, Name: "Demoted"}

	resp := oh.login(t, claims)
	require.Equal(t, "/", resp.Header.Get("Location"))

	_, err := oh.tx.ExecContext(ctx, `UPDATE users SET role = 'staff' WHERE id = $1`, a.ID)
	require.NoError(t, err)

	requireLoginRefused(t, oh.harness, oh.login(t, claims), "saml_required")
}

// A subject that is empty or blank cannot be bound to anything. It is reported
// as the malformed token it is, whatever the setting says, and not as a
// SAML-required refusal.
func TestOIDCCallback_RequireSAML_BlankSubjectIsAnInvalidToken(t *testing.T) {
	for _, on := range []bool{true, false} {
		oh, cleanup := newOIDCHarness(t)
		require.NoError(t, oh.adminSvc.SetSAMLEnabled(context.Background(), on))
		resp := oh.login(t, fakeOIDCClaims{
			Subject: "   ", Email: "blank@test.local", EmailVerified: true, Name: "Blank",
		})
		requireLoginRefused(t, oh.harness, resp, "invalid_id_token")
		cleanup()
	}
}
