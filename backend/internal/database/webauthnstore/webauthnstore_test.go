package webauthnstore_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/webauthnstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/webauthn"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// seedUser makes an owner for the credentials, and REMOVES it afterwards.
//
// The cleanup is not tidiness. This suite shares a database with the rest of
// the package tests, and the setup route decides whether an instance has ever
// been configured by counting every row in users — including disabled and
// deleted ones, deliberately, so that setup cannot reopen. A user left behind
// here makes TestSetupStatus_Needed, TestSetup_CreatesAdmin and
// TestSetup_MissingFields fail in a different package, with an error that
// says nothing about passkeys. The first version of this file left them
// behind and did exactly that.
//
// The credentials go with the user through ON DELETE CASCADE, which is also
// worth having exercised on every run.
func seedUser(t *testing.T, db *testutil.DB, q *dbgen.Queries) uuid.UUID {
	t.Helper()
	us := userstore.New(q)
	u := user.User{
		ID:          uuid.New(),
		Email:       "passkey-" + uuid.NewString() + "@test.local",
		DisplayName: "Passkey Owner",
		Role:        user.RoleStaff,
	}
	require.NoError(t, us.Create(context.Background(), u))
	t.Cleanup(func() {
		_, err := db.SQL.Exec(`DELETE FROM users WHERE id = $1`, u.ID)
		require.NoError(t, err)
	})
	return u.ID
}

func cred(userID uuid.UUID, id []byte) webauthn.Credential {
	return webauthn.Credential{
		UserID:         userID,
		CredentialID:   id,
		PublicKey:      []byte("cose-public-key"),
		Transports:     []string{"usb", "nfc"},
		AAGUID:         []byte("model-identifier"),
		BackupEligible: false,
		BackupState:    false,
		Name:           "YubiKey on the keyring",
	}
}

func TestWebAuthnStore(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q := dbgen.New(db.SQL)
	store := webauthnstore.New(q)
	ctx := context.Background()

	t.Run("a registered credential comes back with every field", func(t *testing.T) {
		uid := seedUser(t, db, q)
		id := []byte("credential-" + uuid.NewString())
		require.NoError(t, store.Create(ctx, cred(uid, id)))

		got, err := store.GetByCredentialID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, uid, got.UserID)
		require.Equal(t, []byte("cose-public-key"), got.PublicKey)
		require.Equal(t, []string{"usb", "nfc"}, got.Transports,
			"transports did not survive; the sign-in challenge needs them")
		require.Equal(t, []byte("model-identifier"), got.AAGUID)
		require.Equal(t, "YubiKey on the keyring", got.Name)
		require.Zero(t, got.SignCount)
		require.Nil(t, got.LastUsedAt, "never used, so there is no last-used time to report")
	})

	// The mapping is keyed to a constraint name. A name that is wrong makes
	// the mapping silently never fire, so the violation is caused here rather
	// than assumed — the same lesson the SLA category mapping taught.
	t.Run("the same credential twice is refused as a duplicate, not a raw error", func(t *testing.T) {
		uid := seedUser(t, db, q)
		id := []byte("credential-" + uuid.NewString())
		require.NoError(t, store.Create(ctx, cred(uid, id)))

		err := store.Create(ctx, cred(uid, id))
		require.ErrorIs(t, err, webauthn.ErrCredentialExists,
			"a duplicate credential id came back as something other than ErrCredentialExists: %v", err)
	})

	// Credential ids are globally unique per the specification, so the same
	// one arriving under a different account is the same refusal.
	t.Run("and refused across accounts too", func(t *testing.T) {
		a, b := seedUser(t, db, q), seedUser(t, db, q)
		id := []byte("credential-" + uuid.NewString())
		require.NoError(t, store.Create(ctx, cred(a, id)))
		require.ErrorIs(t, store.Create(ctx, cred(b, id)), webauthn.ErrCredentialExists)
	})

	t.Run("an unknown credential is not found rather than an error", func(t *testing.T) {
		_, err := store.GetByCredentialID(ctx, []byte("never-registered"))
		require.ErrorIs(t, err, webauthn.ErrNotFound)
	})

	t.Run("counting is what lets a passkey satisfy the MFA requirement", func(t *testing.T) {
		uid := seedUser(t, db, q)
		n, err := store.CountForUser(ctx, uid)
		require.NoError(t, err)
		require.Zero(t, n)

		require.NoError(t, store.Create(ctx, cred(uid, []byte("c1-"+uuid.NewString()))))
		require.NoError(t, store.Create(ctx, cred(uid, []byte("c2-"+uuid.NewString()))))
		n, err = store.CountForUser(ctx, uid)
		require.NoError(t, err)
		require.EqualValues(t, 2, n, "a person registers several keys on purpose")
	})

	t.Run("touch records the counter and the time", func(t *testing.T) {
		uid := seedUser(t, db, q)
		id := []byte("credential-" + uuid.NewString())
		require.NoError(t, store.Create(ctx, cred(uid, id)))
		before, err := store.GetByCredentialID(ctx, id)
		require.NoError(t, err)

		require.NoError(t, store.Touch(ctx, before.ID, 42))

		after, err := store.GetByCredentialID(ctx, id)
		require.NoError(t, err)
		require.EqualValues(t, 42, after.SignCount)
		require.NotNil(t, after.LastUsedAt, "a used credential still reports no last-used time")
	})

	// An id is not an authorisation. The statement matches on both columns,
	// so somebody else's credential id is simply not found — which is also
	// the right answer to give, since confirming it exists would be telling
	// the caller something they were not entitled to.
	t.Run("one account cannot delete another's credential", func(t *testing.T) {
		owner, stranger := seedUser(t, db, q), seedUser(t, db, q)
		id := []byte("credential-" + uuid.NewString())
		require.NoError(t, store.Create(ctx, cred(owner, id)))
		stored, err := store.GetByCredentialID(ctx, id)
		require.NoError(t, err)

		require.ErrorIs(t, store.Delete(ctx, stored.ID, stranger), webauthn.ErrNotFound,
			"a stranger removed somebody else's credential")

		_, err = store.GetByCredentialID(ctx, id)
		require.NoError(t, err, "the credential was deleted despite the refusal")

		require.NoError(t, store.Delete(ctx, stored.ID, owner))
		_, err = store.GetByCredentialID(ctx, id)
		require.ErrorIs(t, err, webauthn.ErrNotFound, "the owner's own delete did not take")
	})

	// Both flags, not just BackupState: eligible-but-not-currently-backed-up
	// is a credential that CAN sync, which is what an administrator asking
	// "is our second factor hardware-backed" needs to know.
	t.Run("synced credentials are distinguishable from hardware keys", func(t *testing.T) {
		uid := seedUser(t, db, q)
		hw := cred(uid, []byte("hw-"+uuid.NewString()))
		synced := cred(uid, []byte("sync-"+uuid.NewString()))
		synced.BackupEligible, synced.BackupState = true, true
		synced.Transports = []string{"internal", "hybrid"}
		require.NoError(t, store.Create(ctx, hw))
		require.NoError(t, store.Create(ctx, synced))

		got, err := store.ListByUser(ctx, uid)
		require.NoError(t, err)
		require.Len(t, got, 2)
		byName := map[bool]webauthn.Credential{}
		for _, c := range got {
			byName[c.Synced()] = c
		}
		require.False(t, byName[false].BackupEligible, "the hardware key reported itself as syncable")
		require.True(t, byName[true].Synced(), "the synced credential was indistinguishable from a hardware key")
	})
}
