package server_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Changing your own password writes your password, and not your role.
//
// It used to read the whole row, spend 45 to 66 milliseconds hashing, and
// write every column back — and the account holder picks the moment. So
// demoting a compromised account while its owner had a password change in
// flight wrote "admin" back over the demotion, and they signed in again, with
// their new password, as an administrator. Looping password changes costs an
// attacker nothing, so the window is as wide as they want it.
//
// The interleaving is staged by changing the role first and then letting the
// password write land: from the database's point of view that is the same
// thing, and it is the part that has to hold.
func TestSetPassword_DoesNotWriteBackAnOldRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// A staff account that gets demoted while a password change is in flight.
	target, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "in-flight@test.local", DisplayName: "In Flight",
		Role: user.RoleStaff, Password: "a-real-passphrase",
	})
	require.NoError(t, err)

	require.NoError(t, h.userSvc.SetRole(ctx, target.ID, user.RoleUser))
	require.NoError(t, h.userSvc.SetPassword(ctx, target.ID, "a-newer-passphrase"))

	stored, err := h.userSvc.GetByIDAdmin(ctx, target.ID)
	require.NoError(t, err)
	require.Equal(t, user.RoleUser, stored.Role,
		"the password change wrote an older copy of the row back and restored the role")

	// And it really did change the password.
	_, err = h.userSvc.VerifyPassword(ctx, "in-flight@test.local", "a-newer-passphrase")
	require.NoError(t, err)
}

// Confirming an MFA enrolment writes the MFA state, and nothing else.
func TestConfirmMFAEnrollment_DoesNotWriteBackAnOldRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	target, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "enrolling@test.local", DisplayName: "Enrolling",
		Role: user.RoleStaff, Password: "a-real-passphrase",
	})
	require.NoError(t, err)

	require.NoError(t, h.userSvc.SetRole(ctx, target.ID, user.RoleUser))
	enrollMFA(t, ctx, h.userSvc, target.ID)

	stored, err := h.userSvc.GetByIDAdmin(ctx, target.ID)
	require.NoError(t, err)
	require.Equal(t, user.RoleUser, stored.Role,
		"the enrolment wrote an older copy of the row back and restored the role")
	require.True(t, stored.MFAEnabled, "and it should still have enrolled them")
}

// A federated sign-in updates the name and address the provider sends, and
// leaves the role and the password alone.
//
// A login is something the account holder triggers whenever they like, so a
// demotion or an administrator's password reset landing in that window was
// written back by their next sign-in — and they were an administrator again,
// with their old password.
func TestFederatedSignIn_DoesNotWriteBackAnOldRoleOrPassword(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	target, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "federated@test.local", DisplayName: "Federated",
		Role: user.RoleAdmin, Password: "the-old-passphrase",
		OIDCSubject: "sub-abc",
	})
	require.NoError(t, err)

	// An administrator deals with the account: demote, then reset the
	// password.
	require.NoError(t, h.userSvc.SetRole(ctx, target.ID, user.RoleUser))
	require.NoError(t, h.userSvc.AdminSetPassword(ctx, target.ID, "the-reset-passphrase", nil))

	// And then they sign in again through the identity provider.
	back, err := h.userSvc.UpsertOIDCUser(ctx, "sub-abc", "federated@test.local", "Federated")
	require.NoError(t, err)
	require.Equal(t, target.ID, back.ID)

	stored, err := h.userSvc.GetByIDAdmin(ctx, target.ID)
	require.NoError(t, err)
	require.Equal(t, user.RoleUser, stored.Role,
		"the sign-in restored the role the administrator had just taken away")

	_, err = h.userSvc.VerifyPassword(ctx, "federated@test.local", "the-old-passphrase")
	require.Error(t, err, "the sign-in restored the password the administrator had just reset")
}
