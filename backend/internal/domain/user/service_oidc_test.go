package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
)

// These tests pin the behaviour UpsertOIDCUser *must* have. UpsertSAMLUser is
// the reference implementation: a federated login creates RoleUser accounts,
// refuses disallowed email domains, and never silently takes over an account
// that belongs to someone else.
//
// Several cases fail against the current implementation. That is deliberate —
// each failure is a defect, named in the case comment.

// errStoreDown is a transient store failure, distinct from errFakeNotFound.
var errStoreDown = errors.New("connection reset by peer")

// seedUser builds a persisted-looking user record.
func seedUser(email, name string, role user.Role) user.User {
	return user.User{
		ID:          uuid.New(),
		Email:       email,
		DisplayName: name,
		Role:        role,
		CreatedAt:   time.Now().Add(-time.Hour),
		UpdatedAt:   time.Now().Add(-time.Hour),
	}
}

func TestUpsertOIDCUser(t *testing.T) {
	existingBySubject := seedUser("old@example.com", "Old Name", user.RoleUser)
	existingBySubject.OIDCSubject = "sub-sync"

	keepEmail := seedUser("keep@example.com", "Keep Email", user.RoleUser)
	keepEmail.OIDCSubject = "sub-keep-email"

	keepName := seedUser("keepname@example.com", "Keep Name", user.RoleUser)
	keepName.OIDCSubject = "sub-keep-name"

	disabledBySubject := seedUser("disabled-sub@example.com", "Disabled Sub", user.RoleUser)
	disabledBySubject.OIDCSubject = "sub-disabled"
	disabledBySubject.Disabled = true

	disabledByEmail := seedUser("disabled-mail@example.com", "Disabled Mail", user.RoleUser)
	disabledByEmail.Disabled = true

	linkable := seedUser("linkme@example.com", "Link Me", user.RoleUser)

	adminAccount := seedUser("admin@example.com", "The Admin", user.RoleAdmin)

	boundToOtherOIDC := seedUser("taken@example.com", "Taken", user.RoleUser)
	boundToOtherOIDC.OIDCSubject = "sub-already-bound"

	samlAccount := seedUser("samluser@example.com", "SAML User", user.RoleUser)
	samlAccount.SAMLSubject = "saml-nameid-1"

	cases := []struct {
		name string
		seed []user.User

		subject     string
		email       string
		displayName string

		errGetByOIDC  error
		errGetByEmail error

		wantErr   bool
		wantErrIs error

		wantRole    user.Role // "" = not checked
		wantEmail   string    // "" = not checked
		wantName    string    // "" = not checked
		wantSubject string    // "" = not checked
		wantSameID  *uuid.UUID

		wantCreates int
		wantUpdates int
	}{
		{
			// DEFECT 1: a brand-new federated identity must land as RoleUser,
			// exactly as UpsertSAMLUser does. The OIDC path hard-codes RoleStaff,
			// handing agent-level access to every account the IdP will vouch for.
			name:        "new user is created with RoleUser",
			subject:     "sub-new",
			email:       "new@example.com",
			displayName: "New User",
			wantRole:    user.RoleUser,
			wantEmail:   "new@example.com",
			wantName:    "New User",
			wantSubject: "sub-new",
			wantCreates: 1,
		},
		{
			name:        "email is lower-cased and trimmed on create",
			subject:     "sub-normalise",
			email:       "  MiXeD@Example.COM  ",
			displayName: "  Mixed Case  ",
			wantEmail:   "mixed@example.com",
			wantName:    "Mixed Case",
			wantCreates: 1,
		},
		{
			name:        "existing user found by subject has profile synced",
			seed:        []user.User{existingBySubject},
			subject:     "sub-sync",
			email:       "New@Example.com",
			displayName: "New Name",
			wantEmail:   "new@example.com",
			wantName:    "New Name",
			wantRole:    user.RoleUser,
			wantSameID:  &existingBySubject.ID,
			wantUpdates: 1,
		},
		{
			// DEFECT 5: email sync is unconditional, so an IdP that stops
			// releasing the email claim blanks the stored address. The next such
			// user then collides on the UNIQUE email index and the login 500s.
			name:        "empty email claim must not blank the stored email",
			seed:        []user.User{keepEmail},
			subject:     "sub-keep-email",
			email:       "",
			displayName: "Keep Email",
			wantEmail:   "keep@example.com",
			wantSameID:  &keepEmail.ID,
			wantUpdates: 1,
		},
		{
			name:        "empty display name claim must not blank the stored name",
			seed:        []user.User{keepName},
			subject:     "sub-keep-name",
			email:       "keepname@example.com",
			displayName: "",
			wantName:    "Keep Name",
			wantSameID:  &keepName.ID,
			wantUpdates: 1,
		},
		{
			// DEFECT 4: VerifyPassword refuses !u.IsActive(); the federated path
			// must refuse it too, or disabling an account does not lock anybody out.
			name:        "disabled user matched by subject is rejected",
			seed:        []user.User{disabledBySubject},
			subject:     "sub-disabled",
			email:       "disabled-sub@example.com",
			displayName: "Disabled Sub",
			wantErr:     true,
			wantCreates: 0,
			wantUpdates: 0,
		},
		{
			// DEFECT 4, via the email-linking branch.
			name:        "disabled user matched by email is rejected",
			seed:        []user.User{disabledByEmail},
			subject:     "sub-brand-new",
			email:       "disabled-mail@example.com",
			displayName: "Disabled Mail",
			wantErr:     true,
			wantCreates: 0,
			wantUpdates: 0,
		},
		{
			name:        "links to an existing RoleUser account by email",
			seed:        []user.User{linkable},
			subject:     "sub-link",
			email:       "linkme@example.com",
			displayName: "Link Me",
			wantSameID:  &linkable.ID,
			wantRole:    user.RoleUser,
			wantSubject: "sub-link",
			wantUpdates: 1,
			wantCreates: 0,
		},
		{
			// DEFECT 3: binding an unknown OIDC subject to a privileged local
			// account on the strength of a matching email is account takeover.
			// Whoever controls the IdP (or any tenant of a shared IdP) can mint a
			// token for admin@example.com and inherit the admin account.
			name:        "refuses to link onto an admin account",
			seed:        []user.User{adminAccount},
			subject:     "sub-attacker",
			email:       "admin@example.com",
			displayName: "Not The Admin",
			wantErr:     true,
			wantCreates: 0,
			wantUpdates: 0,
		},
		{
			// DEFECT 3: the account is already bound to a different OIDC identity;
			// rebinding it silently transfers the account to the new subject.
			name:        "refuses to rebind an account bound to another OIDC subject",
			seed:        []user.User{boundToOtherOIDC},
			subject:     "sub-different",
			email:       "taken@example.com",
			displayName: "Taken",
			wantErr:     true,
			wantCreates: 0,
			wantUpdates: 0,
		},
		{
			// DEFECT 3: same for an account that already federates via SAML.
			name:        "refuses to link onto an account with a SAML subject",
			seed:        []user.User{samlAccount},
			subject:     "sub-oidc-over-saml",
			email:       "samluser@example.com",
			displayName: "SAML User",
			wantErr:     true,
			wantCreates: 0,
			wantUpdates: 0,
		},
		{
			// DEFECT 6: `if err == nil { ... }` treats every store failure as
			// "no such subject", so a blip on the subject lookup falls through to
			// the email-link branch and then creates a duplicate account.
			name:          "store failure on the subject lookup propagates",
			subject:       "sub-store-down",
			email:         "storedown@example.com",
			displayName:   "Store Down",
			errGetByOIDC:  errStoreDown,
			wantErr:       true,
			wantErrIs:     errStoreDown,
			wantCreates:   0,
			wantUpdates:   0,
			errGetByEmail: nil,
		},
		{
			// DEFECT 6, on the email lookup.
			name:          "store failure on the email lookup propagates",
			subject:       "sub-store-down-2",
			email:         "storedown2@example.com",
			displayName:   "Store Down 2",
			errGetByEmail: errStoreDown,
			wantErr:       true,
			wantErrIs:     errStoreDown,
			wantCreates:   0,
			wantUpdates:   0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeUserStore()
			for _, u := range tc.seed {
				store.seed(u)
			}
			store.errGetByOIDC = tc.errGetByOIDC
			store.errGetByEmail = tc.errGetByEmail

			svc := user.NewService(store)

			got, err := svc.UpsertOIDCUser(context.Background(), tc.subject, tc.email, tc.displayName)

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrIs != nil {
					require.ErrorIs(t, err, tc.wantErrIs)
				}
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tc.wantCreates, store.creates, "store.Create call count")
			require.Equal(t, tc.wantUpdates, store.updates, "store.Update call count")

			if tc.wantErr {
				return
			}

			if tc.wantRole != "" {
				require.Equal(t, tc.wantRole, got.Role, "role")
			}
			if tc.wantEmail != "" {
				require.Equal(t, tc.wantEmail, got.Email, "email")
			}
			if tc.wantName != "" {
				require.Equal(t, tc.wantName, got.DisplayName, "display name")
			}
			if tc.wantSubject != "" {
				require.Equal(t, tc.wantSubject, got.OIDCSubject, "oidc subject")
			}
			if tc.wantSameID != nil {
				require.Equal(t, *tc.wantSameID, got.ID, "must reuse the existing account")
			}

			// The returned user must match what was persisted.
			stored, err := store.GetByID(context.Background(), got.ID)
			require.NoError(t, err)
			require.Equal(t, got.Email, stored.Email)
			require.Equal(t, got.DisplayName, stored.DisplayName)
			require.Equal(t, got.Role, stored.Role)
		})
	}
}

// TestUpsertSAMLUser_RejectsDisabled pins the same account-state rule on the
// SAML path. This is a pre-existing gap, not a regression introduced by OIDC:
// UpsertSAMLUser also never consults IsActive(), so a disabled account still
// authenticates through the IdP.
func TestUpsertSAMLUser_RejectsDisabled(t *testing.T) {
	store := newFakeUserStore()
	u := seedUser("disabled-saml@example.com", "Disabled SAML", user.RoleUser)
	u.SAMLSubject = "saml-disabled"
	u.Disabled = true
	store.seed(u)

	svc := user.NewService(store)

	_, err := svc.UpsertSAMLUser(
		context.Background(),
		"saml-disabled",
		"disabled-saml@example.com",
		"Disabled SAML",
		nil,
	)
	require.Error(t, err, "a disabled account must not authenticate via SAML")
	require.Equal(t, 0, store.updates, "a rejected login must not touch the record")
	require.Equal(t, 0, store.creates)
}

// TestUpsertSAMLUser_DomainNotAllowed documents the reference behaviour the
// OIDC path is missing (defect 2): the domain allow-list is enforced before a
// new account is created.
func TestUpsertSAMLUser_DomainNotAllowed(t *testing.T) {
	store := newFakeUserStore()
	svc := user.NewService(store)

	_, err := svc.UpsertSAMLUser(
		context.Background(),
		"saml-new",
		"someone@notallowed.example",
		"Someone",
		[]string{"allowed.example"},
	)
	require.ErrorIs(t, err, user.ErrDomainNotAllowed)
	require.Equal(t, 0, store.creates)
}
