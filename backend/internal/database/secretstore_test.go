package database_test

import (
	"context"
	"sync"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSecretStore_InsertIfAbsentAndGet(t *testing.T) {
	db, cleanup := testutil.NewDB(t)
	defer cleanup()
	store := database.NewSecretStore(db.Queries)
	ctx := context.Background()

	// Test: insert a secret and retrieve it
	secret := []byte("this-is-a-32-byte-secret-ok!")
	err := store.InsertIfAbsent(ctx, "jwt_signing", secret)
	require.NoError(t, err)

	retrieved, err := store.Get(ctx, "jwt_signing")
	require.NoError(t, err)
	require.Equal(t, secret, retrieved)
}

func TestSecretStore_InsertIfAbsentRaceCondition(t *testing.T) {
	db, cleanup := testutil.NewDB(t)
	defer cleanup()
	store := database.NewSecretStore(db.Queries)
	ctx := context.Background()

	// Test: 20 goroutines try to insert different values concurrently
	// Only one should succeed (INSERT ... ON CONFLICT DO NOTHING)
	n := 20
	candidates := make([][]byte, n)
	for i := 0; i < n; i++ {
		candidates[i] = []byte{
			byte(i % 256), 1, 2, 3, 4, 5, 6, 7, 8, 9,
			10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
			20, 21, 22, 23, 24, 25, 26, 27, 28, 29,
			30, 31,
		}
	}

	var wg sync.WaitGroup
	results := make([][]byte, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			err := store.InsertIfAbsent(ctx, "jwt_signing", candidates[idx])
			require.NoError(t, err)
			val, err := store.Get(ctx, "jwt_signing")
			require.NoError(t, err)
			results[idx] = val
		}(i)
	}
	wg.Wait()

	// All goroutines should read the same value
	for i := 1; i < n; i++ {
		require.Equal(t, results[0], results[i], "goroutine %d got a different value", i)
	}

	// Verify the value is one of the candidates
	foundIdx := -1
	for i, c := range candidates {
		if string(c) == string(results[0]) {
			foundIdx = i
			break
		}
	}
	require.GreaterOrEqual(t, foundIdx, 0, "retrieved value is not one of the candidates")
}

func TestSecretStore_CheckConstraint(t *testing.T) {
	db, cleanup := testutil.NewDB(t)
	defer cleanup()
	store := database.NewSecretStore(db.Queries)
	ctx := context.Background()

	// Test: inserting a value shorter than 32 bytes should be rejected by the CHECK constraint
	shortSecret := []byte("too-short")
	err := store.InsertIfAbsent(ctx, "jwt_signing", shortSecret)
	require.Error(t, err, "expected CHECK constraint to reject short secret")
}

func TestSecretStore_UnknownName(t *testing.T) {
	db, cleanup := testutil.NewDB(t)
	defer cleanup()
	store := database.NewSecretStore(db.Queries)
	ctx := context.Background()

	// Test: trying to insert with an unknown name should be rejected by the CHECK constraint
	secret := []byte("this-is-a-32-byte-secret-ok!")
	err := store.InsertIfAbsent(ctx, "unknown_secret", secret)
	require.Error(t, err, "expected CHECK constraint to reject unknown name")
}
