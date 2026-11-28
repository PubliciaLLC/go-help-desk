package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// failingFirstMFA is a store whose adopt-the-secret write fails.
type failingFirstMFA struct{ *fakeUserStore }

var errFirstMFA = errors.New("connection refused")

func (failingFirstMFA) SetFirstMFA(context.Context, uuid.UUID, string) (bool, error) {
	return false, errFirstMFA
}

// ConfirmMFAEnrollmentWith's refusals are sentinels (#421). The handler used
// to turn every error that was not ErrMFAAlreadyEnrolled into a 400, so a
// database fault read as "your code is wrong". The two things the person
// did wrong are named; everything else must stay an unnamed fault.
func TestConfirmMFAEnrollmentWith_NamesWhatThePersonGotWrong(t *testing.T) {
	ctx := context.Background()
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "t", AccountName: "a@test.local"})
	require.NoError(t, err)
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	require.NoError(t, err)

	seeded := func() (*fakeUserStore, uuid.UUID) {
		store := newFakeUserStore()
		u := user.User{ID: uuid.New(), Email: "a@test.local"}
		store.seed(u)
		return store, u.ID
	}

	t.Run("nothing staged", func(t *testing.T) {
		store, id := seeded()
		err := user.NewService(store).ConfirmMFAEnrollmentWith(ctx, id, "", code, false)
		require.ErrorIs(t, err, user.ErrMFANotStarted)
		require.NotErrorIs(t, err, user.ErrMFAInvalidCode)
	})
	t.Run("wrong code", func(t *testing.T) {
		store, id := seeded()
		err := user.NewService(store).ConfirmMFAEnrollmentWith(ctx, id, key.Secret(), "000000", false)
		require.ErrorIs(t, err, user.ErrMFAInvalidCode)
		require.NotErrorIs(t, err, user.ErrMFANotStarted)
	})
	t.Run("someone else enrolled first", func(t *testing.T) {
		store, id := seeded()
		u := store.byID[id]
		u.MFAEnabled = true
		store.seed(u)
		err := user.NewService(store).ConfirmMFAEnrollmentWith(ctx, id, key.Secret(), code, false)
		require.ErrorIs(t, err, user.ErrMFAAlreadyEnrolled)
	})
	t.Run("a good code enrols", func(t *testing.T) {
		store, id := seeded()
		require.NoError(t, user.NewService(store).ConfirmMFAEnrollmentWith(ctx, id, key.Secret(), code, false))
	})
	t.Run("a storage fault is none of them", func(t *testing.T) {
		store, id := seeded()
		err := user.NewService(failingFirstMFA{store}).ConfirmMFAEnrollmentWith(ctx, id, key.Secret(), code, false)
		require.ErrorIs(t, err, errFirstMFA)
		require.NotErrorIs(t, err, user.ErrMFANotStarted)
		require.NotErrorIs(t, err, user.ErrMFAInvalidCode)
		require.NotErrorIs(t, err, user.ErrMFAAlreadyEnrolled)
	})
}
