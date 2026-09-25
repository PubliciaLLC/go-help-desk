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
