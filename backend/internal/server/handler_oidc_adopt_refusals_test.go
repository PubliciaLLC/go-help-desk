package server_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Adopting a local account by email hands it to whoever the identity provider
// says owns the address, so it is refused for an account that already belongs
// to another identity. TestOIDCCallback_DoesNotLinkOntoAdmin covers the role
// rule; these are the other two, run against the real store so the statement's
// own conditions are in play and not only the in-memory fake's copy of them.
func TestOIDCCallback_DoesNotAdoptAnAccountAlreadyFederatedElsewhere(t *testing.T) {
	cases := []struct {
		name  string
		input user.CreateUserInput
		// bound reads the identity the account must still carry afterwards.
		bound func(u user.User) string
		want  string
	}{
		{
			name: "SAML subject set",
			input: user.CreateUserInput{
				Email: "saml-person@test.local", DisplayName: "SAML Person",
				Role: user.RoleUser, SAMLSubject: "saml-name-id",
			},
			bound: func(u user.User) string { return u.SAMLSubject },
			want:  "saml-name-id",
		},
		{
			name: "bound to a different OIDC subject",
			input: user.CreateUserInput{
				Email: "oidc-person@test.local", DisplayName: "OIDC Person",
				Role: user.RoleUser, OIDCSubject: "the-real-sub",
			},
			bound: func(u user.User) string { return u.OIDCSubject },
			want:  "the-real-sub",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oh, cleanup := newOIDCHarness(t)
			defer cleanup()
			ctx := context.Background()

			victim, err := oh.userSvc.Create(ctx, tc.input)
			require.NoError(t, err)

			resp := oh.login(t, fakeOIDCClaims{
				Subject:       "attacker-sub",
				Email:         tc.input.Email,
				EmailVerified: true,
				Name:          "Attacker",
			})
			requireLoginRefused(t, oh.harness, resp, "account_link_refused")

			after, err := oh.userSvc.GetByIDAdmin(ctx, victim.ID)
			require.NoError(t, err)
			require.Equal(t, tc.want, tc.bound(after), "the account's own identity was changed")
			require.NotEqual(t, "attacker-sub", after.OIDCSubject, "the attacker's subject was bound")
			require.Equal(t, tc.input.DisplayName, after.DisplayName, "the profile was overwritten")
		})
	}
}
