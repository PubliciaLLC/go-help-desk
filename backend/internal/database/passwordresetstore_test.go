package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/passwordresetstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/passwordreset"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// resetRig is a transaction with a user service, the reset store and a way to
// read back what a redeem left behind.
type resetRig struct {
	ctx   context.Context
	users *user.Service
	store *passwordresetstore.Store
	query func(q string, args ...any) *scanner
}

type scanner struct{ scan func(dest ...any) error }

func newResetRig(t *testing.T) *resetRig {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	t.Cleanup(rollback)
	return &resetRig{
		ctx:   context.Background(),
		users: user.NewService(userstore.New(q), user.WithBcryptCost(4)),
		store: passwordresetstore.New(q),
		query: func(sql string, args ...any) *scanner {
			return &scanner{scan: func(dest ...any) error { return tx.QueryRow(sql, args...).Scan(dest...) }}
		},
	}
}

func (r *resetRig) account(t *testing.T, email string) user.User {
	t.Helper()
	u, err := r.users.Create(r.ctx, user.CreateUserInput{
		Email: email, DisplayName: "Ada", Role: user.RoleUser, Password: "the-old-password"})
	require.NoError(t, err)
	return u
}

func (r *resetRig) hashOf(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var h string
	require.NoError(t, r.query(`SELECT password_hash FROM users WHERE id = $1`, id).scan(&h))
	return h
}

func (r *resetRig) session(t *testing.T, id uuid.UUID) {
	t.Helper()
	require.NoError(t, r.query(
		`INSERT INTO sessions (id, user_id, data, expires_at) VALUES ($1, $2, '\x00', now() + interval '1 day') RETURNING id`,
		uuid.NewString(), id).scan(new(string)))
}

func (r *resetRig) sessions(t *testing.T, id uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, r.query(`SELECT count(*) FROM sessions WHERE user_id = $1`, id).scan(&n))
	return n
}

// #451: spending a link, setting the password and ending every session are one
// statement. A link that works once must change the password, clear a pending
// forced change, and leave no session behind; and only that account's.
func TestPasswordResetStore_RedeemSetsThePasswordAndEndsEverySession(t *testing.T) {
	r := newResetRig(t)
	ada, bob := r.account(t, "ada@reset.test"), r.account(t, "bob@reset.test")
	r.session(t, ada.ID)
	r.session(t, ada.ID)
	r.session(t, bob.ID)
	require.NoError(t, r.query(`UPDATE users SET must_change_password = true WHERE id = $1 RETURNING id`, ada.ID).scan(new(uuid.UUID)))
	before := r.hashOf(t, ada.ID)
	require.NoError(t, r.store.Issue(r.ctx, ada.ID, "hash-a", passwordreset.TokenTTL))

	got, err := r.store.Redeem(r.ctx, "hash-a", "new-hash")

	require.NoError(t, err)
	require.Equal(t, ada.ID, got)
	require.NotEqual(t, before, r.hashOf(t, ada.ID))
	require.Equal(t, "new-hash", r.hashOf(t, ada.ID))
	require.Zero(t, r.sessions(t, ada.ID), "a session survived the password change")
	require.Equal(t, 1, r.sessions(t, bob.ID), "someone else's session was ended")
	var must bool
	require.NoError(t, r.query(`SELECT must_change_password FROM users WHERE id = $1`, ada.ID).scan(&must))
	require.False(t, must, "choosing a password is what a forced change waits for")
}

func TestPasswordResetStore_ALinkWorksOnce(t *testing.T) {
	r := newResetRig(t)
	ada := r.account(t, "ada@reset.test")
	require.NoError(t, r.store.Issue(r.ctx, ada.ID, "hash-a", passwordreset.TokenTTL))

	_, err := r.store.Redeem(r.ctx, "hash-a", "first")
	require.NoError(t, err)
	_, err = r.store.Redeem(r.ctx, "hash-a", "second")

	require.ErrorIs(t, err, passwordreset.ErrTokenInvalid)
	require.Equal(t, "first", r.hashOf(t, ada.ID), "a spent link changed the password again")
}

func TestPasswordResetStore_ANewLinkReplacesTheOldOne(t *testing.T) {
	r := newResetRig(t)
	ada := r.account(t, "ada@reset.test")
	require.NoError(t, r.store.Issue(r.ctx, ada.ID, "first", passwordreset.TokenTTL))
	require.NoError(t, r.store.Issue(r.ctx, ada.ID, "second", passwordreset.TokenTTL))

	_, err := r.store.Redeem(r.ctx, "first", "x")
	require.ErrorIs(t, err, passwordreset.ErrTokenInvalid, "the replaced link must not work")
	_, err = r.store.Redeem(r.ctx, "second", "y")
	require.NoError(t, err)
}

func TestPasswordResetStore_AnExpiredLinkDoesNotWork(t *testing.T) {
	r := newResetRig(t)
	ada := r.account(t, "ada@reset.test")
	before := r.hashOf(t, ada.ID)
	r.session(t, ada.ID)
	require.NoError(t, r.store.Issue(r.ctx, ada.ID, "late", -time.Minute))

	_, err := r.store.Redeem(r.ctx, "late", "x")

	require.ErrorIs(t, err, passwordreset.ErrTokenInvalid)
	require.Equal(t, before, r.hashOf(t, ada.ID))
	require.Equal(t, 1, r.sessions(t, ada.ID), "an expired link ended a session")
}

func TestPasswordResetStore_AnUnknownLinkChangesNothing(t *testing.T) {
	r := newResetRig(t)
	ada := r.account(t, "ada@reset.test")
	before := r.hashOf(t, ada.ID)
	require.NoError(t, r.store.Issue(r.ctx, ada.ID, "real", passwordreset.TokenTTL))

	_, err := r.store.Redeem(r.ctx, "guess", "x")

	require.ErrorIs(t, err, passwordreset.ErrTokenInvalid)
	require.Equal(t, before, r.hashOf(t, ada.ID))
	_, err = r.store.Redeem(r.ctx, "real", "y")
	require.NoError(t, err, "a wrong guess used up the real link")
}

// An account that was disabled or deleted after the mail went out gets nothing
// from the link, and the link is spent either way.
func TestPasswordResetStore_ADisabledOrDeletedAccountIsNotChanged(t *testing.T) {
	for name, stmt := range map[string]string{
		"disabled": `UPDATE users SET disabled = true WHERE id = $1 RETURNING id`,
		"deleted":  `UPDATE users SET deleted_at = now() WHERE id = $1 RETURNING id`,
	} {
		t.Run(name, func(t *testing.T) {
			r := newResetRig(t)
			ada := r.account(t, "ada@reset.test")
			before := r.hashOf(t, ada.ID)
			require.NoError(t, r.store.Issue(r.ctx, ada.ID, "tok", passwordreset.TokenTTL))
			require.NoError(t, r.query(stmt, ada.ID).scan(new(uuid.UUID)))

			_, err := r.store.Redeem(r.ctx, "tok", "x")

			require.ErrorIs(t, err, passwordreset.ErrTokenInvalid)
			require.Equal(t, before, r.hashOf(t, ada.ID))
		})
	}
}
