package userstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/webauthnstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/webauthn"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

func seedFactorUser(t *testing.T, db *testutil.DB, store *userstore.Store, mfaSecret string, mfaEnabled bool) user.User {
	t.Helper()
	u := user.User{
		ID:          uuid.New(),
		Email:       "factors-" + uuid.NewString() + "@test.local",
		DisplayName: "Factor Owner",
		Role:        user.RoleStaff,
		MFASecret:   mfaSecret,
		MFAEnabled:  mfaEnabled,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, store.Create(context.Background(), u))
	t.Cleanup(func() { deleteUser(t, db, u.ID) })
	return u
}

func addPasskey(t *testing.T, passkeys *webauthnstore.Store, userID uuid.UUID) {
	t.Helper()
	require.NoError(t, passkeys.Create(context.Background(), webauthn.Credential{
		UserID:       userID,
		CredentialID: []byte("cred-" + uuid.NewString()),
		PublicKey:    []byte("cose-public-key"),
		Transports:   []string{"usb"},
		AAGUID:       []byte("model"),
		Name:         "a key",
	}))
}

func addSession(t *testing.T, q *dbgen.Queries, userID uuid.UUID) {
	t.Helper()
	require.NoError(t, q.UpsertSession(context.Background(), dbgen.UpsertSessionParams{
		ID:              "sess-" + uuid.NewString(),
		UserID:          database.NullUUID(&userID),
		Data:            []byte("x"),
		LifetimeSeconds: 3600,
	}))
}

func sessionCount(t *testing.T, db *testutil.DB, userID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = $1`, userID).Scan(&n))
	return n
}

// ClearFactors is "clear every second factor, and end every session that
// passed it" as ONE statement (#307 items 2 and 4).
//
// One statement, not three, because a failure between them left an account
// with no factor and its MFAPassed=true sessions alive; and one statement for
// passkeys as well as TOTP because the admin "Reset MFA" button cleared only
// the TOTP columns, leaving a passkey-only account as locked out as before.
func TestClearFactors(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q := dbgen.New(db.SQL)
	store := userstore.New(q)
	passkeys := webauthnstore.New(q)
	ctx := context.Background()

	t.Run("removes the authenticator, every passkey and every session, and only theirs", func(t *testing.T) {
		victim := seedFactorUser(t, db, store, "TOTPSECRET", true)
		bystander := seedFactorUser(t, db, store, "OTHERSECRET", true)
		addPasskey(t, passkeys, victim.ID)
		addPasskey(t, passkeys, victim.ID)
		addPasskey(t, passkeys, bystander.ID)
		addSession(t, q, victim.ID)
		addSession(t, q, victim.ID)
		addSession(t, q, bystander.ID)

		removed, err := store.ClearFactors(ctx, victim.ID)
		require.NoError(t, err)
		require.Equal(t, 2, removed, "the count of passkeys removed is what the audit entry records")

		got, err := store.GetByIDAdmin(ctx, victim.ID)
		require.NoError(t, err)
		require.False(t, got.MFAEnabled)
		require.Empty(t, got.MFASecret)
		n, err := passkeys.CountForUser(ctx, victim.ID)
		require.NoError(t, err)
		require.Zero(t, n, "a passkey survived: a passkey-only account stays locked out")
		require.Zero(t, sessionCount(t, db, victim.ID), "a session that passed the cleared factor survived")

		other, err := store.GetByIDAdmin(ctx, bystander.ID)
		require.NoError(t, err)
		require.True(t, other.MFAEnabled, "another account lost its authenticator")
		n, err = passkeys.CountForUser(ctx, bystander.ID)
		require.NoError(t, err)
		require.EqualValues(t, 1, n, "another account lost a passkey")
		require.Equal(t, 1, sessionCount(t, db, bystander.ID), "another account lost a session")
	})

	t.Run("a passkey-only account is cleared", func(t *testing.T) {
		u := seedFactorUser(t, db, store, "", false)
		addPasskey(t, passkeys, u.ID)

		removed, err := store.ClearFactors(ctx, u.ID)
		require.NoError(t, err)
		require.Equal(t, 1, removed)
		n, err := passkeys.CountForUser(ctx, u.ID)
		require.NoError(t, err)
		require.Zero(t, n)
	})

	t.Run("an account with nothing to clear is not an error", func(t *testing.T) {
		u := seedFactorUser(t, db, store, "", false)
		removed, err := store.ClearFactors(ctx, u.ID)
		require.NoError(t, err)
		require.Zero(t, removed)
	})

	// The same promise ClearMFA made after #306's review: a nonexistent id must
	// not look like a successful clear, because an audit entry follows one.
	t.Run("a nonexistent user is ErrNotFound", func(t *testing.T) {
		_, err := store.ClearFactors(ctx, uuid.New())
		require.Error(t, err)
		require.True(t, errors.Is(err, user.ErrNotFound), "got %v", err)
	})
}
