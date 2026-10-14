package user_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// fixedExpiry is a PasswordExpiry with a deadline chosen by the test.
type fixedExpiry struct {
	deadline time.Time
	ok       bool
}

func (f fixedExpiry) PasswordRehashDeadline(context.Context) (time.Time, bool) {
	return f.deadline, f.ok
}

func pastDeadline() fixedExpiry   { return fixedExpiry{deadline: time.Now().Add(-time.Hour), ok: true} }
func futureDeadline() fixedExpiry { return fixedExpiry{deadline: time.Now().Add(time.Hour), ok: true} }

// seedAdminWithCost is seedWithCost for an administrator.
func seedAdminWithCost(t *testing.T, store *fakeUserStore, email, password string, cost int) user.User {
	t.Helper()
	u := seedWithCost(t, store, email, password, cost)
	u.Role = user.RoleAdmin
	store.seed(u)
	return u
}

func TestVerifyPasswordForSignIn_BeforeDeadlineRehashes(t *testing.T) {
	store := newFakeUserStore()
	u := seedWithCost(t, store, "old@example.com", "correct horse", 4)
	svc := user.NewService(store, user.WithBcryptCost(6), user.WithPasswordExpiry(futureDeadline()))

	_, err := svc.VerifyPasswordForSignIn(context.Background(), u.Email, "correct horse")
	require.NoError(t, err)
	require.Equal(t, 6, storedCost(t, store, u.ID))
}

func TestVerifyPasswordForSignIn_AfterDeadlineRefusesOldHash(t *testing.T) {
	store := newFakeUserStore()
	u := seedWithCost(t, store, "old@example.com", "correct horse", 4)
	svc := user.NewService(store, user.WithBcryptCost(6), user.WithPasswordExpiry(pastDeadline()))

	got, err := svc.VerifyPasswordForSignIn(context.Background(), u.Email, "correct horse")
	require.ErrorIs(t, err, user.ErrPasswordResetRequired)
	require.Equal(t, user.User{}, got)
	require.Zero(t, store.rehashCalls, "a refused sign-in must not quietly upgrade the hash")
	require.Equal(t, 4, storedCost(t, store, u.ID))
}

func TestVerifyPasswordForSignIn_AfterDeadlineWrongPasswordIsStillInvalidCredentials(t *testing.T) {
	store := newFakeUserStore()
	u := seedWithCost(t, store, "old@example.com", "correct horse", 4)
	svc := user.NewService(store, user.WithBcryptCost(6), user.WithPasswordExpiry(pastDeadline()))

	_, err := svc.VerifyPasswordForSignIn(context.Background(), u.Email, "wrong")
	require.Error(t, err)
	require.NotErrorIs(t, err, user.ErrPasswordResetRequired,
		"the reset message is only for someone who knows the password; a guesser must not learn the hash is old")
}

func TestVerifyPasswordForSignIn_AfterDeadlineCurrentCostSignsIn(t *testing.T) {
	store := newFakeUserStore()
	u := seedWithCost(t, store, "new@example.com", "correct horse", 6)
	svc := user.NewService(store, user.WithBcryptCost(6), user.WithPasswordExpiry(pastDeadline()))

	_, err := svc.VerifyPasswordForSignIn(context.Background(), u.Email, "correct horse")
	require.NoError(t, err)
}

// An account that signed in before the deadline was rehashed then, so it
// never meets the refusal.
func TestVerifyPasswordForSignIn_RehashedAccountIsUnaffectedAfterDeadline(t *testing.T) {
	store := newFakeUserStore()
	u := seedWithCost(t, store, "old@example.com", "correct horse", 4)
	ctx := context.Background()

	before := user.NewService(store, user.WithBcryptCost(6), user.WithPasswordExpiry(futureDeadline()))
	_, err := before.VerifyPasswordForSignIn(ctx, u.Email, "correct horse")
	require.NoError(t, err)

	after := user.NewService(store, user.WithBcryptCost(6), user.WithPasswordExpiry(pastDeadline()))
	_, err = after.VerifyPasswordForSignIn(ctx, u.Email, "correct horse")
	require.NoError(t, err)
}

func TestVerifyPasswordForSignIn_LastActiveAdminIsExempt(t *testing.T) {
	store := newFakeUserStore()
	admin := seedAdminWithCost(t, store, "admin@example.com", "correct horse", 4)
	svc := user.NewService(store, user.WithBcryptCost(6), user.WithPasswordExpiry(pastDeadline()))

	_, err := svc.VerifyPasswordForSignIn(context.Background(), admin.Email, "correct horse")
	require.NoError(t, err)
	require.Equal(t, 6, storedCost(t, store, admin.ID), "the exempt administrator is rehashed as before")
}

// A disabled administrator is not someone else who can get in, so it does not
// end the exemption.
func TestVerifyPasswordForSignIn_DisabledAdminDoesNotEndTheExemption(t *testing.T) {
	store := newFakeUserStore()
	admin := seedAdminWithCost(t, store, "admin@example.com", "correct horse", 4)
	gone := seedAdminWithCost(t, store, "gone@example.com", "correct horse", 4)
	gone.Disabled = true
	store.seed(gone)
	svc := user.NewService(store, user.WithBcryptCost(6), user.WithPasswordExpiry(pastDeadline()))

	_, err := svc.VerifyPasswordForSignIn(context.Background(), admin.Email, "correct horse")
	require.NoError(t, err)
}

// With two active administrators neither is the last, so both are refused.
func TestVerifyPasswordForSignIn_AdministratorIsRefusedWhenAnotherExists(t *testing.T) {
	store := newFakeUserStore()
	a := seedAdminWithCost(t, store, "a@example.com", "correct horse", 4)
	seedAdminWithCost(t, store, "b@example.com", "correct horse", 4)
	svc := user.NewService(store, user.WithBcryptCost(6), user.WithPasswordExpiry(pastDeadline()))

	_, err := svc.VerifyPasswordForSignIn(context.Background(), a.Email, "correct horse")
	require.ErrorIs(t, err, user.ErrPasswordResetRequired)
}

// No deadline recorded means no enforcement: failing closed would lock
// everyone out over a missing row.
func TestVerifyPasswordForSignIn_NoDeadlineNoRefusal(t *testing.T) {
	store := newFakeUserStore()
	u := seedWithCost(t, store, "old@example.com", "correct horse", 4)
	for name, svc := range map[string]*user.Service{
		"no policy":       user.NewService(store, user.WithBcryptCost(6)),
		"policy, no date": user.NewService(store, user.WithBcryptCost(6), user.WithPasswordExpiry(fixedExpiry{})),
	} {
		_, err := svc.VerifyPasswordForSignIn(context.Background(), u.Email, "correct horse")
		require.NoError(t, err, name)
	}
}

// VerifyPassword (used to confirm the current password while changing it)
// never refuses: changing the password is the cure.
func TestVerifyPassword_NeverRefusesForExpiry(t *testing.T) {
	store := newFakeUserStore()
	u := seedWithCost(t, store, "old@example.com", "correct horse", 4)
	svc := user.NewService(store, user.WithBcryptCost(6), user.WithPasswordExpiry(pastDeadline()))

	_, err := svc.VerifyPassword(context.Background(), u.Email, "correct horse")
	require.NoError(t, err)
}

// The refusal must spend the same bcrypt work as any wrong password, so it
// cannot tell a stranger anything the failure paths do not (#475).
func TestVerifyPasswordForSignIn_RefusalSpendsTheSameWorkAsAWrongPassword(t *testing.T) {
	const target = 6
	store := newFakeUserStore()
	seedWithCost(t, store, "old@example.com", "correct horse", 4)

	svc := user.NewService(store, user.WithBcryptCost(target), user.WithPasswordExpiry(pastDeadline()))
	var work int
	user.SetCompareHook(svc, func(hash, plain []byte) error {
		if c, err := bcrypt.Cost(hash); err == nil {
			work += 1 << c
		}
		return bcrypt.CompareHashAndPassword(hash, plain)
	})

	work = 0
	_, err := svc.VerifyPasswordForSignIn(context.Background(), "old@example.com", "wrong guess")
	require.Error(t, err)
	wrong := work

	work = 0
	_, err = svc.VerifyPasswordForSignIn(context.Background(), "nobody@example.com", "wrong guess")
	require.Error(t, err)
	unknown := work

	work = 0
	_, err = svc.VerifyPasswordForSignIn(context.Background(), "old@example.com", "correct horse")
	require.ErrorIs(t, err, user.ErrPasswordResetRequired)

	require.Equal(t, 1<<target, wrong)
	require.Equal(t, wrong, unknown)
	require.Equal(t, wrong, work, "the refusal must cost what a wrong password costs")
}
