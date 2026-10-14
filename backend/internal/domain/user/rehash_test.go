package user_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// seedWithCost stores an active local account whose hash was made at cost.
func seedWithCost(t *testing.T, store *fakeUserStore, email, password string, cost int) user.User {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	require.NoError(t, err)
	u := user.User{ID: uuid.New(), Email: email, DisplayName: "Rehash", Role: user.RoleUser, PasswordHash: string(h)}
	store.seed(u)
	return u
}

func storedCost(t *testing.T, store *fakeUserStore, id uuid.UUID) int {
	t.Helper()
	c, err := bcrypt.Cost([]byte(store.byID[id].PasswordHash))
	require.NoError(t, err)
	return c
}

func TestVerifyPassword_RehashesLowCostHashOnSuccess(t *testing.T) {
	store := newFakeUserStore()
	u := seedWithCost(t, store, "old@example.com", "correct horse", 4)
	svc := user.NewService(store, user.WithBcryptCost(6))

	_, err := svc.VerifyPassword(context.Background(), u.Email, "correct horse")
	require.NoError(t, err)

	require.Equal(t, 6, storedCost(t, store, u.ID))
	// The new hash still accepts the same password, and is not rewritten again.
	_, err = svc.VerifyPassword(context.Background(), u.Email, "correct horse")
	require.NoError(t, err)
	require.Equal(t, 1, store.rehashCalls, "a hash already at the target cost must not be rewritten")
}

func TestVerifyPassword_NoRehashOnWrongPasswordOrCurrentCost(t *testing.T) {
	store := newFakeUserStore()
	low := seedWithCost(t, store, "low@example.com", "correct horse", 4)
	cur := seedWithCost(t, store, "cur@example.com", "correct horse", 6)
	svc := user.NewService(store, user.WithBcryptCost(6))

	_, err := svc.VerifyPassword(context.Background(), low.Email, "wrong")
	require.Error(t, err)
	_, err = svc.VerifyPassword(context.Background(), cur.Email, "correct horse")
	require.NoError(t, err)

	require.Zero(t, store.rehashCalls)
	require.Equal(t, 4, storedCost(t, store, low.ID))
}

// A rehash computed from the old password must not overwrite a password that
// was changed while it was being computed.
func TestVerifyPassword_RehashDoesNotClobberConcurrentPasswordChange(t *testing.T) {
	store := newFakeUserStore()
	u := seedWithCost(t, store, "race@example.com", "old password", 4)
	newHash, err := bcrypt.GenerateFromPassword([]byte("changed meanwhile"), 4)
	require.NoError(t, err)

	svc := user.NewService(store, user.WithBcryptCost(6))
	// The compare hook runs after the sign-in's read and before its write:
	// change the password in that gap.
	user.SetCompareHook(svc, func(hash, plain []byte) error {
		err := bcrypt.CompareHashAndPassword(hash, plain)
		if err == nil {
			require.NoError(t, store.SetPasswordHash(context.Background(), u.ID, string(newHash)))
		}
		return err
	})

	_, err = svc.VerifyPassword(context.Background(), u.Email, "old password")
	require.NoError(t, err)
	require.Equal(t, string(newHash), store.byID[u.ID].PasswordHash)
}

func TestVerifyPassword_RehashFailureDoesNotFailSignIn(t *testing.T) {
	store := newFakeUserStore()
	store.errRehash = errors.New("database down")
	u := seedWithCost(t, store, "fail@example.com", "correct horse", 4)
	svc := user.NewService(store, user.WithBcryptCost(6))

	got, err := svc.VerifyPassword(context.Background(), u.Email, "correct horse")
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)
	require.Equal(t, 1, store.rehashCalls)
	require.Equal(t, 4, storedCost(t, store, u.ID))
}

// The rehash write must survive the client hanging up: it is detached from the
// request context. The fake ignores ctx, so check what it was handed.
func TestVerifyPassword_RehashContextIsDetached(t *testing.T) {
	store := newFakeUserStore()
	u := seedWithCost(t, store, "cancel@example.com", "correct horse", 4)
	svc := user.NewService(store, user.WithBcryptCost(6))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.VerifyPassword(ctx, u.Email, "correct horse")
	require.NoError(t, err)
	require.NoError(t, store.rehashCtxErr, "the rehash write saw a cancelled context")
	require.Equal(t, 1, store.rehashCalls)
	require.Equal(t, 6, storedCost(t, store, u.ID))
}

// During the move to a higher cost, a wrong password on an account still on
// the old cost used to finish in a fraction of the time an unknown address
// takes. Pinned structurally rather than by clock: every failure path must
// spend the same total bcrypt work, counted as 2^cost per comparison.
func TestVerifyPassword_EveryFailurePathSpendsTheSameWork(t *testing.T) {
	const target = 6
	store := newFakeUserStore()
	seedWithCost(t, store, "old@example.com", "correct horse", 4)
	seedWithCost(t, store, "new@example.com", "correct horse", target)
	off := seedWithCost(t, store, "off@example.com", "correct horse", 4)
	off.Disabled = true
	store.seed(off)
	// An account that signs in only through SSO stores no password at all (the
	// column is NOT NULL DEFAULT ''), and a damaged row is not a bcrypt hash.
	// bcrypt refuses both before any hashing (#476).
	store.seed(user.User{ID: uuid.New(), Email: "sso@example.com", DisplayName: "SSO", Role: user.RoleUser, OIDCSubject: "sub-1"})
	store.seed(user.User{ID: uuid.New(), Email: "bad@example.com", DisplayName: "Bad", Role: user.RoleUser, PasswordHash: "not-a-bcrypt-hash"})

	svc := user.NewService(store, user.WithBcryptCost(target))
	var work int
	user.SetCompareHook(svc, func(hash, plain []byte) error {
		// A hash bcrypt cannot read is refused before any key expansion, so
		// the comparison spends no work.
		if c, err := bcrypt.Cost(hash); err == nil {
			work += 1 << c
		}
		return bcrypt.CompareHashAndPassword(hash, plain)
	})

	for _, email := range []string{
		"nobody@example.com", // unknown address
		"old@example.com",    // wrong password, hash below target
		"new@example.com",    // wrong password, hash at target
		"off@example.com",    // disabled account
		"sso@example.com",    // SSO-only account, no password hash
		"bad@example.com",    // hash bcrypt cannot read
	} {
		work = 0
		_, err := svc.VerifyPassword(context.Background(), email, "wrong guess")
		require.Error(t, err, email)
		require.Equal(t, 1<<target, work, "%s: failure must cost one target-cost comparison", email)
	}
}
