package user_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// A service user is the identity an OAuth client acts as. It never signs in,
// so every path that would give it a way in is refused with ErrServiceAccount
// before anything is written (#420). The database CHECK is the backstop; these
// are the answers a caller can act on.
func seedServiceUser(store *fakeUserStore) user.User {
	u := user.User{
		ID:             uuid.New(),
		Email:          "oauth-x@service-account.invalid",
		DisplayName:    "Nagios",
		Role:           user.RoleStaff,
		ServiceAccount: true,
	}
	store.seed(u)
	return u
}

func TestServiceAccount_RefusedEveryWayIn(t *testing.T) {
	ctx := context.Background()
	store := newFakeUserStore()
	svc := user.NewService(store)
	u := seedServiceUser(store)

	t.Run("set password", func(t *testing.T) {
		require.ErrorIs(t, svc.SetPassword(ctx, u.ID, "a-long-enough-passphrase"), user.ErrServiceAccount)
	})
	t.Run("generate MFA secret", func(t *testing.T) {
		_, _, err := svc.GenerateMFASecret(ctx, u.ID, "Help Desk", false)
		require.ErrorIs(t, err, user.ErrServiceAccount)
	})
	t.Run("enrol MFA", func(t *testing.T) {
		_, _, err := svc.EnrollMFA(ctx, u.ID, "Help Desk", false)
		require.ErrorIs(t, err, user.ErrServiceAccount)
	})
	t.Run("confirm MFA", func(t *testing.T) {
		require.ErrorIs(t, svc.ConfirmMFAEnrollment(ctx, u.ID, "123456"), user.ErrServiceAccount)
	})
	t.Run("administrator resets the password", func(t *testing.T) {
		au := &fakeAuditStore{}
		svc := user.NewService(store, user.WithAuditStore(au))
		require.ErrorIs(t, svc.AdminSetPassword(ctx, u.ID, "a-long-enough-passphrase", nil), user.ErrServiceAccount)
		require.Empty(t, au.entries, "a refused reset is not recorded as done")
	})
	t.Run("administrator resets MFA", func(t *testing.T) {
		au := &fakeAuditStore{}
		svc := user.NewService(store, user.WithAuditStore(au))
		require.ErrorIs(t, svc.ResetMFA(ctx, u.ID, nil), user.ErrServiceAccount)
		require.Empty(t, au.entries)
	})
	require.Zero(t, store.updates, "nothing may have been written")
}

// An OIDC login whose address matches a service user's must not adopt it. The
// store's login lookup already does not return service users; this is the rule
// itself, so that it still holds if a lookup ever does.
func TestServiceAccount_OIDCDoesNotAdoptByEmail(t *testing.T) {
	store := newFakeUserStore()
	svc := user.NewService(store)
	u := seedServiceUser(store)

	_, err := svc.UpsertOIDCUser(context.Background(), "sub-1", u.Email, "Mallory")
	require.ErrorIs(t, err, user.ErrAccountLinkRefused)
	require.Zero(t, store.updates)
}
