package server_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Email addresses must be ASCII: lower-casing folds some non-ASCII letters
// into ASCII ones, so such an address is refused rather than compared.

func TestOIDC_RefusesANonASCIIEmail(t *testing.T) {
	t.Run("new account, allow-list set", func(t *testing.T) {
		oh, cleanup := newOIDCHarness(t)
		defer cleanup()
		require.NoError(t, oh.adminSvc.SetRaw(context.Background(),
			admin.KeyAllowedEmailDomains, []byte(`["xi.test"]`)))

		resp := oh.login(t, fakeOIDCClaims{Subject: "sub-new", Email: "newbie@xİ.test", EmailVerified: true, Name: "N"})
		requireLoginRefused(t, oh.harness, resp, "domain_not_allowed")
		_, ok := findUser(t, oh.harness, "newbie@xi.test")
		require.False(t, ok)
	})

	t.Run("new account, no allow-list", func(t *testing.T) {
		oh, cleanup := newOIDCHarness(t)
		defer cleanup()

		resp := oh.login(t, fakeOIDCClaims{Subject: "sub-new", Email: "newbie@xİ.test", EmailVerified: true, Name: "N"})
		requireLoginRefused(t, oh.harness, resp, "invalid_email")
		_, ok := findUser(t, oh.harness, "newbie@xi.test")
		require.False(t, ok)
	})

	t.Run("first sign-in does not link an existing account", func(t *testing.T) {
		for _, role := range []user.Role{user.RoleUser, user.RoleStaff} {
			t.Run(string(role), func(t *testing.T) {
				oh, cleanup := newOIDCHarness(t)
				defer cleanup()
				ctx := context.Background()
				existing, err := oh.userSvc.Create(ctx, user.CreateUserInput{Email: "jim@xi.test", DisplayName: "Jim", Role: role})
				require.NoError(t, err)

				for _, email := range []string{"jim@xİ.test", "jİm@xi.test", "Kim@xi.test"} {
					resp := oh.login(t, fakeOIDCClaims{Subject: "other-sub", Email: email, EmailVerified: true, Name: "Other"})
					requireLoginRefused(t, oh.harness, resp, "invalid_email")
				}

				after, err := oh.userSvc.GetByIDAdmin(ctx, existing.ID)
				require.NoError(t, err)
				require.Empty(t, after.OIDCSubject, "the existing account must stay unlinked")
			})
		}
	})
}

func TestSAML_RefusesANonASCIIEmail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	existing, err := h.userSvc.Create(ctx, user.CreateUserInput{Email: "jim@xi.test", DisplayName: "Jim", Role: user.RoleUser})
	require.NoError(t, err)

	for _, allowed := range [][]string{nil, {"xi.test"}} {
		_, err = h.userSvc.UpsertSAMLUser(ctx, "saml-new", "newbie@xİ.test", "N", allowed)
		require.Error(t, err)
		_, err = h.userSvc.UpsertSAMLUser(ctx, "saml-other", "jim@xİ.test", "J", allowed)
		require.Error(t, err)
	}
	_, ok := findUser(t, h, "newbie@xi.test")
	require.False(t, ok)
	after, err := h.userSvc.GetByIDAdmin(ctx, existing.ID)
	require.NoError(t, err)
	require.Empty(t, after.SAMLSubject)
}

func TestCreateUser_RefusesANonASCIIEmail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	_, err := h.userSvc.Create(context.Background(),
		user.CreateUserInput{Email: "jim@xİ.test", DisplayName: "Jim", Role: user.RoleUser})
	require.ErrorIs(t, err, user.ErrValidation)
	_, ok := findUser(t, h, "jim@xi.test")
	require.False(t, ok)
}

func TestGuestResend_RefusesANonASCIIEmail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enableGuests(t, h)
	tk, _ := seedGuestTicketFor(t, h, "jim@xi.test")

	_, err := h.ticketSvc.GuestTicketIDFor(ctx, tk.TrackingNumber, "jim@xi.test")
	require.NoError(t, err, "precondition: the ASCII address resolves")
	_, err = h.ticketSvc.GuestTicketIDFor(ctx, tk.TrackingNumber, "jim@xİ.test")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)
}
