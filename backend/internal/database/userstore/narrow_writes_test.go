package userstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Each narrow write touches the column it names and leaves the rest alone.
//
// This is the property that matters, and it is tested here rather than
// through the service because it is a property of the SQL. The service tests
// cannot fail against a read-modify-write version: staging a genuine stale
// read from outside would need the write to pause mid-flight, and a test that
// changes the role first and then calls the write has no window at all. Two
// such tests were written and kept only as documentation for that reason.
//
// A statement that grew an extra column would fail this.
//
// The fault it guards against: SetPassword used to read the whole row, spend
// 45 to 66 milliseconds on bcrypt, and write every column back — and the
// account holder picks the moment. Demote a compromised account while its
// owner has a password change in flight, and "admin" is written back over the
// demotion.
func TestNarrowWrites_TouchOnlyWhatTheyName(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	store := userstore.New(dbgen.New(db.SQL))
	ctx := context.Background()

	seed := func(t *testing.T) user.User {
		t.Helper()
		u := user.User{
			ID:          uuid.New(),
			Email:       "narrow-" + uuid.NewString() + "@test.local",
			DisplayName: "Narrow Write",
			// Staff, deliberately: a widened statement would most likely
			// write a constant, and "admin" is the constant that matters. A
			// fixture that was already admin could not tell the difference —
			// the first version of this test was, and could not.
			Role:         user.RoleStaff,
			PasswordHash: "$2a$10$originalhashoriginalhashoriginalhashoriginalhashoriginal",
			MFASecret:    "ORIGINALSECRET",
			MFAEnabled:   true,
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
		}
		require.NoError(t, store.Create(ctx, u))
		t.Cleanup(func() { deleteUser(t, db, u.ID) })
		return u
	}

	t.Run("SetPasswordHash", func(t *testing.T) {
		u := seed(t)
		require.NoError(t, store.SetPasswordHash(ctx, u.ID, "$2a$10$replacement"))

		got, err := store.GetByIDAdmin(ctx, u.ID)
		require.NoError(t, err)
		require.Equal(t, "$2a$10$replacement", got.PasswordHash)
		require.Equal(t, u.Role, got.Role, "it wrote the role as well")
		require.Equal(t, u.Email, got.Email)
		require.Equal(t, u.DisplayName, got.DisplayName)
		require.Equal(t, u.MFASecret, got.MFASecret, "it wrote the MFA secret as well")
		require.Equal(t, u.MFAEnabled, got.MFAEnabled)
	})

	t.Run("SetMFA", func(t *testing.T) {
		u := seed(t)
		require.NoError(t, store.SetMFA(ctx, u.ID, "NEWSECRET", false))

		got, err := store.GetByIDAdmin(ctx, u.ID)
		require.NoError(t, err)
		require.Equal(t, "NEWSECRET", got.MFASecret)
		require.False(t, got.MFAEnabled)
		require.Equal(t, u.Role, got.Role, "it wrote the role as well")
		require.Equal(t, u.PasswordHash, got.PasswordHash, "it wrote the password as well")
		require.Equal(t, u.Email, got.Email)
	})

	t.Run("UpdateProfile", func(t *testing.T) {
		u := seed(t)
		require.NoError(t, store.UpdateProfile(ctx, u.ID, "renamed-"+u.Email, "Renamed"))

		got, err := store.GetByIDAdmin(ctx, u.ID)
		require.NoError(t, err)
		require.Equal(t, "renamed-"+u.Email, got.Email)
		require.Equal(t, "Renamed", got.DisplayName)
		require.Equal(t, u.Role, got.Role, "it wrote the role as well")
		require.Equal(t, u.PasswordHash, got.PasswordHash, "it wrote the password as well")
		require.Equal(t, u.MFASecret, got.MFASecret)
	})

	t.Run("AdoptOIDCSubject", func(t *testing.T) {
		u := seed(t)
		applied, err := store.AdoptOIDCSubject(ctx, u.ID, "sub-"+uuid.NewString(), "From The IdP")
		require.NoError(t, err)
		require.True(t, applied)

		got, err := store.GetByIDAdmin(ctx, u.ID)
		require.NoError(t, err)
		require.Equal(t, "From The IdP", got.DisplayName)
		require.Equal(t, u.Role, got.Role, "adopting an account wrote the role as well")
		require.Equal(t, u.PasswordHash, got.PasswordHash,
			"adopting an account wrote the password as well")
		require.Equal(t, u.Email, got.Email)
		require.Equal(t, u.MFASecret, got.MFASecret)
	})

	// The rules are in the statement, so they are answered by the row as it
	// is now — not by the copy the caller read before it decided.
	//
	// The read-check-write version could not do this. An identity provider
	// login is something the account holder triggers at will, so promoting,
	// disabling or deleting an account while one is in flight put the change
	// back and adopted the account anyway. An administrator is the one thing
	// adoption may never take, because it hands the account to whoever the
	// provider says owns that address.
	t.Run("AdoptOIDCSubject refuses on the row as it is now", func(t *testing.T) {
		cases := []struct {
			name   string
			change func(t *testing.T, id uuid.UUID)
		}{
			{"promoted to administrator", func(t *testing.T, id uuid.UUID) {
				_, err := db.SQL.Exec(`UPDATE users SET role = 'admin' WHERE id = $1`, id)
				require.NoError(t, err)
			}},
			{"disabled", func(t *testing.T, id uuid.UUID) {
				_, err := db.SQL.Exec(`UPDATE users SET disabled = TRUE WHERE id = $1`, id)
				require.NoError(t, err)
			}},
			{"deleted", func(t *testing.T, id uuid.UUID) {
				_, err := db.SQL.Exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, id)
				require.NoError(t, err)
			}},
			{"already federates via SAML", func(t *testing.T, id uuid.UUID) {
				_, err := db.SQL.Exec(`UPDATE users SET saml_subject = $2 WHERE id = $1`,
					id, "saml-"+uuid.NewString())
				require.NoError(t, err)
			}},
			{"bound to another OIDC subject", func(t *testing.T, id uuid.UUID) {
				_, err := db.SQL.Exec(`UPDATE users SET oidc_subject = $2 WHERE id = $1`,
					id, "other-"+uuid.NewString())
				require.NoError(t, err)
			}},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				u := seed(t)
				tc.change(t, u.ID)

				subject := "sub-" + uuid.NewString()
				applied, err := store.AdoptOIDCSubject(ctx, u.ID, subject, "From The IdP")
				require.NoError(t, err)
				require.False(t, applied, "the account was adopted anyway")

				var bound string
				require.NoError(t, db.SQL.QueryRow(
					`SELECT oidc_subject FROM users WHERE id = $1`, u.ID).Scan(&bound))
				require.NotEqual(t, subject, bound, "the subject was bound to it anyway")
			})
		}
	})

	t.Run("SyncFederated", func(t *testing.T) {
		u := seed(t)
		require.NoError(t, store.SyncFederated(ctx, u.ID, "idp-"+u.Email, "From The IdP"))

		got, err := store.GetByIDAdmin(ctx, u.ID)
		require.NoError(t, err)
		require.Equal(t, "idp-"+u.Email, got.Email)
		require.Equal(t, "From The IdP", got.DisplayName)
		require.Equal(t, u.Role, got.Role,
			"a sign-in wrote the role, so it can restore one an administrator just removed")
		require.Equal(t, u.PasswordHash, got.PasswordHash,
			"a sign-in wrote the password, so it can restore one an administrator just reset")
	})
}

// An administrator's "reset MFA" beats a confirmation that was already in
// flight.
//
// ConfirmMFAEnrollment used to read the row, validate a TOTP code against the
// secret it found, and write that same secret back with the flag. Validating
// takes time, and a reset committing inside that window was undone: the
// cleared secret came back and MFA was re-enabled with exactly the
// authenticator the reset existed to revoke. Found by the session-B review of
// #292; it had no production caller, which is why it survived.
//
// Staged rather than raced: the reset is applied between the read and the
// write, which is the whole window expressed as two statements.
func TestEnableMFAIfStillEnrolled_ResetWins(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	store := userstore.New(dbgen.New(db.SQL))
	ctx := context.Background()

	seed := func(t *testing.T) user.User {
		t.Helper()
		u := user.User{
			ID:          uuid.New(),
			Email:       "mfa-" + uuid.NewString() + "@test.local",
			DisplayName: "MFA Race",
			Role:        user.RoleStaff,
			MFASecret:   "ORIGINALSECRET",
			MFAEnabled:  false,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		}
		require.NoError(t, store.Create(ctx, u))
		t.Cleanup(func() { deleteUser(t, db, u.ID) })
		return u
	}

	t.Run("an undisturbed confirmation still works", func(t *testing.T) {
		u := seed(t)
		applied, err := store.EnableMFAIfStillEnrolled(ctx, u.ID)
		require.NoError(t, err)
		require.True(t, applied)

		got, err := store.GetByIDAdmin(ctx, u.ID)
		require.NoError(t, err)
		require.True(t, got.MFAEnabled)
		require.Equal(t, "ORIGINALSECRET", got.MFASecret, "it rewrote the secret")
	})

	t.Run("a reset in the window refuses the confirmation", func(t *testing.T) {
		u := seed(t)

		// The administrator's reset, landing while the code is being checked.
		require.NoError(t, store.ClearMFA(ctx, u.ID))

		applied, err := store.EnableMFAIfStillEnrolled(ctx, u.ID)
		require.NoError(t, err)
		require.False(t, applied, "the confirmation went through after the reset")

		got, err := store.GetByIDAdmin(ctx, u.ID)
		require.NoError(t, err)
		require.False(t, got.MFAEnabled,
			"MFA is on again after an administrator reset it")
		require.Empty(t, got.MFASecret,
			"the cleared secret came back, so the revoked authenticator still works")
	})
}
