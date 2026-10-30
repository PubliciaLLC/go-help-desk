package userstore_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #338: two first-time TOTP enrolments confirmed together must not both win.
//
// The handler's guard reads "no factor yet" and the write adopts the staged
// secret, and those were two statements. Every confirm that read the account
// before the first write landed went on to write its own secret over it, each
// answered success, and the last one held the account — so whoever had
// confirmed first was told they were enrolled with a secret that no longer
// worked.
//
// Run against a real connection pool rather than the usual rolled-back
// transaction: the race is between concurrent statements, and a single
// transaction serialises them by construction. The row is deleted afterwards.
func TestConfirmFirstMFAEnrollment_OnlyOneConcurrentConfirmWins(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	store := userstore.New(dbgen.New(db.SQL))
	svc := user.NewService(store)
	id := seedUserForClaim(t, store)
	defer deleteUser(t, db, id)

	const confirms = 20
	secrets := make([]string, confirms)
	codes := make([]string, confirms)
	for i := range confirms {
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "race@test.local"})
		require.NoError(t, err)
		secrets[i] = key.Secret()
		codes[i], err = totp.GenerateCode(secrets[i], time.Now())
		require.NoError(t, err)
	}

	var (
		mu      sync.Mutex
		winners []int
		refused int
		wg      sync.WaitGroup
		start   = make(chan struct{})
	)
	for i := range confirms {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := svc.ConfirmMFAEnrollmentWith(context.Background(), id, secrets[i], codes[i], false)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners = append(winners, i)
			case errors.Is(err, user.ErrMFAAlreadyEnrolled):
				refused++
			default:
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()

	require.Len(t, winners, 1, "%d concurrent first enrolments all reported success", len(winners))
	require.Equal(t, confirms-1, refused)

	u, err := store.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.True(t, u.MFAEnabled)
	require.Equal(t, secrets[winners[0]], u.MFASecret,
		"the stored secret is not the one the winning confirm was told it enrolled")
}

// Rotation is not first enrolment: an account that already has a TOTP, whose
// owner has proved it, replaces the secret. The conditional write must not
// refuse that.
func TestConfirmMFAEnrollment_RotationStillReplacesTheSecret(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	store := userstore.New(dbgen.New(db.SQL))
	svc := user.NewService(store)
	id := seedUserForClaim(t, store)
	defer deleteUser(t, db, id)

	// allowReplace=true is what the handler passes for a session that proved
	// the current factor.
	enrol := func() string {
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "rotate@test.local"})
		require.NoError(t, err)
		code, err := totp.GenerateCode(key.Secret(), time.Now())
		require.NoError(t, err)
		require.NoError(t, svc.ConfirmMFAEnrollmentWith(context.Background(), id, key.Secret(), code, true))
		return key.Secret()
	}
	enrol()
	second := enrol()

	u, err := store.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, second, u.MFASecret)
}

// The same rule without the race: a first-enrolment confirm arriving after
// the account already has a TOTP is refused, and the existing secret stays.
func TestConfirmFirstMFAEnrollment_RefusedOnceAnAccountHasATOTP(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	store := userstore.New(dbgen.New(db.SQL))
	svc := user.NewService(store)
	id := seedUserForClaim(t, store)
	defer deleteUser(t, db, id)

	newCode := func() (string, string) {
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "late@test.local"})
		require.NoError(t, err)
		code, err := totp.GenerateCode(key.Secret(), time.Now())
		require.NoError(t, err)
		return key.Secret(), code
	}
	first, code := newCode()
	require.NoError(t, svc.ConfirmMFAEnrollmentWith(context.Background(), id, first, code, false))

	late, code := newCode()
	err := svc.ConfirmMFAEnrollmentWith(context.Background(), id, late, code, false)
	require.ErrorIs(t, err, user.ErrMFAAlreadyEnrolled)

	u, err := store.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, first, u.MFASecret)
}
