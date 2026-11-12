package userstore_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/webauthnstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/webauthn"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #617: a first factor is written under a lock on the user row.
//
// #338 made a first TOTP confirm conditional on "no TOTP yet", which stops two
// confirms from both winning and says nothing about a passkey. Both doors now
// take the user row first and ask "does this account hold any factor?" after
// getting it, so whichever writes second sees what the first wrote.
//
// Deterministic, not timed: the test holds the row in its own transaction,
// starts the write, waits until Postgres reports the write is queued behind
// that transaction, lets the other factor commit, then releases. The write that
// was waiting must then refuse. A real pool, not the rolled-back transaction
// the other tests use, because the race is between two connections.

type factorRig struct {
	t       *testing.T
	db      *testutil.DB
	users   *userstore.Store
	keys    *webauthnstore.Store
	userID  uuid.UUID
	holder  *sql.Tx
	holderQ *dbgen.Queries
}

func newFactorRig(t *testing.T) *factorRig {
	t.Helper()
	db, closeDB := testutil.NewIsolatedDB(t)
	// A cleanup, so it runs after the holder is rolled back; closing the pool
	// waits on a connection a failed test left blocked.
	t.Cleanup(closeDB)

	r := &factorRig{
		t:     t,
		db:    db,
		users: userstore.NewWithDB(dbgen.New(db.SQL), db.SQL),
		keys:  webauthnstore.NewWithDB(dbgen.New(db.SQL), db.SQL),
	}
	r.userID = seedUserForClaim(t, r.users)
	t.Cleanup(func() { deleteUser(t, db, r.userID) })
	return r
}

// hold takes the user row in a transaction of the test's own, as a factor write
// that has not committed yet would.
func (r *factorRig) hold() {
	r.t.Helper()
	tx, err := r.db.SQL.BeginTx(context.Background(), nil)
	require.NoError(r.t, err)
	r.t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.Exec(`SELECT id FROM users WHERE id = $1 FOR UPDATE`, r.userID)
	require.NoError(r.t, err)
	r.holder, r.holderQ = tx, dbgen.New(tx)
}

// waitQueued blocks until something is waiting on the holder's transaction.
func (r *factorRig) waitQueued() {
	r.t.Helper()
	var pid int
	require.NoError(r.t, r.holder.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid))
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		require.NoError(r.t, r.db.SQL.QueryRow(
			`SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, pid).Scan(&n))
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatal("the factor write never queued behind the user row: it took no lock")
}

func (r *factorRig) holderAddsPasskey() {
	r.t.Helper()
	err := r.holderQ.CreateWebAuthnCredential(context.Background(), dbgen.CreateWebAuthnCredentialParams{
		UserID: r.userID, CredentialID: []byte("held-" + uuid.NewString()), PublicKey: []byte("pk"),
		Transports: []string{}, Aaguid: []byte{}, Name: "held",
	})
	require.NoError(r.t, err)
}

func (r *factorRig) holderAddsTOTP() {
	r.t.Helper()
	_, err := r.holder.Exec(`UPDATE users SET mfa_secret = 'HELDSECRET', mfa_enabled = true WHERE id = $1`, r.userID)
	require.NoError(r.t, err)
}

func (r *factorRig) passkeyCount() int64 {
	r.t.Helper()
	n, err := r.keys.CountForUser(context.Background(), r.userID)
	require.NoError(r.t, err)
	return n
}

func (r *factorRig) newPasskey() webauthn.Credential {
	return webauthn.Credential{
		UserID: r.userID, CredentialID: []byte("late-" + uuid.NewString()), PublicKey: []byte("pk"),
		Transports: []string{}, AAGUID: []byte{}, Name: "late",
	}
}

// A passkey committed while a TOTP confirm waits: the confirm must refuse, as
// it does when the passkey was there before it started.
func TestFirstFactor_TOTPConfirmLosesToAPasskeyCommittedWhileItWaited(t *testing.T) {
	r := newFactorRig(t)
	svc := user.NewService(r.users)
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "race@test.local"})
	require.NoError(t, err)
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	require.NoError(t, err)

	r.hold()
	done := make(chan error, 1)
	go func() {
		done <- svc.ConfirmMFAEnrollmentWith(context.Background(), r.userID, key.Secret(), code, false)
	}()
	r.waitQueued()
	r.holderAddsPasskey()
	require.NoError(t, r.holder.Commit())

	require.ErrorIs(t, <-done, user.ErrMFAAlreadyEnrolled)
	u, err := r.users.GetByID(context.Background(), r.userID)
	require.NoError(t, err)
	require.False(t, u.MFAEnabled, "a TOTP secret was adopted next to a passkey that won the race")
}

// The other way round: a TOTP committed while a passkey registration waits.
func TestFirstFactor_PasskeyLosesToATOTPCommittedWhileItWaited(t *testing.T) {
	r := newFactorRig(t)

	r.hold()
	done := make(chan error, 1)
	go func() { done <- r.keys.CreateFirstFactor(context.Background(), r.newPasskey()) }()
	r.waitQueued()
	r.holderAddsTOTP()
	require.NoError(t, r.holder.Commit())

	require.ErrorIs(t, <-done, webauthn.ErrAlreadyProtected)
	require.Zero(t, r.passkeyCount(), "a passkey was written next to a TOTP that won the race")
}

// Passkey against passkey, which the issue does not mention.
func TestFirstFactor_PasskeyLosesToAnotherPasskeyCommittedWhileItWaited(t *testing.T) {
	r := newFactorRig(t)

	r.hold()
	done := make(chan error, 1)
	go func() { done <- r.keys.CreateFirstFactor(context.Background(), r.newPasskey()) }()
	r.waitQueued()
	r.holderAddsPasskey()
	require.NoError(t, r.holder.Commit())

	require.ErrorIs(t, <-done, webauthn.ErrAlreadyProtected)
	require.EqualValues(t, 1, r.passkeyCount(), "both passkeys were written")
}

// With nothing racing, the first factor goes in, and the second guarded write
// is refused: the rule is about "any factor", not "a factor in flight".
func TestFirstFactor_FirstPasskeyIsWrittenAndTheNextGuardedOneRefused(t *testing.T) {
	r := newFactorRig(t)
	ctx := context.Background()

	require.NoError(t, r.keys.CreateFirstFactor(ctx, r.newPasskey()))
	require.ErrorIs(t, r.keys.CreateFirstFactor(ctx, r.newPasskey()), webauthn.ErrAlreadyProtected)
	require.EqualValues(t, 1, r.passkeyCount())

	// A session that proved its factor adds another with the ordinary write.
	require.NoError(t, r.keys.Create(ctx, r.newPasskey()))
	require.EqualValues(t, 2, r.passkeyCount())
}

// The duplicate-credential mapping still applies on the guarded path.
func TestFirstFactor_DuplicateCredentialIsStillReportedAsOne(t *testing.T) {
	r := newFactorRig(t)
	ctx := context.Background()
	c := r.newPasskey()
	require.NoError(t, r.keys.Create(ctx, c))

	other := seedUserForClaim(t, r.users)
	t.Cleanup(func() { deleteUser(t, r.db, other) })
	c.UserID = other
	require.ErrorIs(t, r.keys.CreateFirstFactor(ctx, c), webauthn.ErrCredentialExists)
}
