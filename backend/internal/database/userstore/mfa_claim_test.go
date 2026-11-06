package userstore_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The TOTP attempt budget holds when the guesses arrive together.
//
// The control used to be three statements — read the lock, check the code,
// count the failure — with a window between the first and the last. Every
// request that started before the first UPDATE landed read "not locked" and
// went on to verify a guess. Measured against the real server: forty parallel
// wrong codes, thirty-six of them verified, against a limit of five. An
// attacker who already holds the password, which is the exact case MFA exists
// for, got a few hundred guesses per fifteen-minute window instead of five.
//
// Run against a real connection pool rather than the usual rolled-back
// transaction, because the race is between concurrent statements and a single
// transaction serialises them by construction — which is exactly why the
// defect survived the existing tests. The row is deleted afterwards.
//
// Sent in parallel deliberately. One at a time, the old code passed.
func TestClaimMFAAttempt_HoldsTheBudgetUnderConcurrentAttempts(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	store := userstore.New(dbgen.New(db.SQL))
	id := seedUserForClaim(t, store)
	defer deleteUser(t, db, id)

	const attempts = 40
	var (
		mu      sync.Mutex
		allowed int
		refused int
		wg      sync.WaitGroup
		start   = make(chan struct{})
	)

	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			count, _, err := store.ClaimMFAAttempt(context.Background(), id,
				user.MFAMaxFailedAttempts, user.MFALockDuration)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			// The same rule the service applies: the attempt that spends the
			// last of the budget still gets to check a code, the next one
			// does not.
			if count <= user.MFAMaxFailedAttempts {
				allowed++
			} else {
				refused++
			}
		}()
	}
	close(start)
	wg.Wait()

	require.Equal(t, attempts, allowed+refused)
	require.Equal(t, user.MFAMaxFailedAttempts, allowed,
		"%d of %d concurrent attempts were let through against a budget of %d",
		allowed, attempts, user.MFAMaxFailedAttempts)
}

// A lock that has run its course gives the account a fresh budget, and one
// that has not does NOT move further away while it is being hammered.
//
// Both halves matter. Without the reset, the count stays at the maximum after
// the lock expires and the next single attempt re-locks immediately, so the
// owner never gets back in. Without the hold, an attacker pushes the deadline
// out with every guess and locks the owner out for as long as they keep
// trying — a denial of service built out of the control meant to prevent one.
func TestClaimMFAAttempt_TheLockExpiresAndDoesNotSlideAway(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	store := userstore.New(dbgen.New(db.SQL))
	id := seedUserForClaim(t, store)
	defer deleteUser(t, db, id)

	ctx := context.Background()

	// Spend the budget. A very short lock, so the expiry half can be observed
	// without waiting fifteen minutes.
	const lock = 2 * time.Second
	var lockedUntil *time.Time
	for i := 1; i <= user.MFAMaxFailedAttempts; i++ {
		count, until, err := store.ClaimMFAAttempt(ctx, id, user.MFAMaxFailedAttempts, lock)
		require.NoError(t, err)
		require.Equal(t, i, count)
		lockedUntil = until
	}
	require.NotNil(t, lockedUntil, "spending the budget should have set a lock")

	// Keep hammering. The count rises; the deadline must not.
	for range 5 {
		count, until, err := store.ClaimMFAAttempt(ctx, id, user.MFAMaxFailedAttempts, lock)
		require.NoError(t, err)
		require.Greater(t, count, user.MFAMaxFailedAttempts)
		require.NotNil(t, until)
		require.WithinDuration(t, *lockedUntil, *until, time.Millisecond,
			"an attacker moved the lock further away just by trying again")
	}

	// And once it has run out, the account starts over.
	time.Sleep(lock + 250*time.Millisecond)
	count, until, err := store.ClaimMFAAttempt(ctx, id, user.MFAMaxFailedAttempts, lock)
	require.NoError(t, err)
	require.Equal(t, 1, count, "an expired lock must reset the count, or one attempt re-locks the account")
	require.Nil(t, until)
}

func seedUserForClaim(t *testing.T, store *userstore.Store) uuid.UUID {
	t.Helper()
	u := user.User{
		ID:          uuid.New(),
		Email:       "mfa-claim-" + uuid.NewString() + "@test.local",
		DisplayName: "MFA Claim Test",
		Role:        user.RoleStaff,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, store.Create(context.Background(), u))
	return u.ID
}

func deleteUser(t *testing.T, db *testutil.DB, id uuid.UUID) {
	t.Helper()
	_, err := db.SQL.Exec(`DELETE FROM users WHERE id = $1`, id)
	require.NoError(t, err)
}
