package user_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
)

func admin(passwordHash, samlSubject, oidcSubject string) user.User {
	return user.User{
		ID:           uuid.New(),
		Email:        "admin@example.com",
		DisplayName:  "Admin",
		Role:         user.RoleAdmin,
		PasswordHash: passwordHash,
		SAMLSubject:  samlSubject,
		OIDCSubject:  oidcSubject,
	}
}

func TestStrandedAdmins(t *testing.T) {
	cases := []struct {
		name          string
		admins        []user.User
		samlReachable bool
		oidcReachable bool
		wantStranded  int
	}{
		{
			name:          "no admins",
			admins:        nil,
			samlReachable: true,
			oidcReachable: true,
			wantStranded:  0,
		},
		{
			name:          "local admin unaffected by either provider",
			admins:        []user.User{admin("hash", "", "")},
			samlReachable: false,
			oidcReachable: false,
			wantStranded:  0,
		},
		{
			// The exact case #300 measured: a passwordless OIDC-only
			// administrator, and OIDC is the change being made.
			name:          "OIDC-only admin stranded when OIDC becomes unreachable",
			admins:        []user.User{admin("", "", "sub-123")},
			samlReachable: false,
			oidcReachable: false,
			wantStranded:  1,
		},
		{
			name:          "OIDC-only admin fine while OIDC stays reachable",
			admins:        []user.User{admin("", "", "sub-123")},
			samlReachable: false,
			oidcReachable: true,
			wantStranded:  0,
		},
		{
			// A SAML subject is not a channel through OIDC — the two
			// providers are not interchangeable for a given account.
			name:          "SAML-only admin stranded by disabling OIDC, since SAML was never their channel",
			admins:        []user.User{admin("", "saml-sub", "")},
			samlReachable: false,
			oidcReachable: false,
			wantStranded:  1,
		},
		{
			name:          "SAML-only admin fine while SAML stays reachable",
			admins:        []user.User{admin("", "saml-sub", "")},
			samlReachable: true,
			oidcReachable: false,
			wantStranded:  0,
		},
		{
			name:          "admin with both subjects needs only one provider reachable",
			admins:        []user.User{admin("", "saml-sub", "oidc-sub")},
			samlReachable: false,
			oidcReachable: true,
			wantStranded:  0,
		},
		{
			name:          "admin with both subjects stranded only when both providers go dark",
			admins:        []user.User{admin("", "saml-sub", "oidc-sub")},
			samlReachable: false,
			oidcReachable: false,
			wantStranded:  1,
		},
		{
			// Mixed instance: one admin has a password (unaffected), one
			// does not (stranded) — the partial case #300's option 2/3
			// distinguishes from the total-lockout case.
			name: "one of two admins stranded",
			admins: []user.User{
				admin("hash", "", ""),
				admin("", "", "sub-123"),
			},
			samlReachable: false,
			oidcReachable: false,
			wantStranded:  1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := user.StrandedAdmins(tc.admins, tc.samlReachable, tc.oidcReachable)
			require.Len(t, got, tc.wantStranded)
		})
	}
}
