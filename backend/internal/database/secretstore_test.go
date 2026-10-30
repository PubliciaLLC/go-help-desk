package database_test

import (
	"context"
	"crypto/rand"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSecretStore_InsertIfAbsentAndGet(t *testing.T) {
	db, cleanup := testutil.NewDB(t)
	defer cleanup()
	// In a transaction that is rolled back: a committed jwt_signing row would
	// leak into every test that follows, since the table holds one per name.
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	store := database.NewSecretStore(q)
	ctx := context.Background()
	// Also inside the transaction: a key already in this database is not touched.
	_, err := tx.ExecContext(ctx, "DELETE FROM instance_secrets")
	require.NoError(t, err)

	// Test: insert a secret and retrieve it
	secret := []byte("this-is-exactly-32-bytes-long!!!")
	err = store.InsertIfAbsent(ctx, "jwt_signing", secret)
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

	// Needs the real pool (concurrent connections), so it commits. It deletes
	// only the row it created: if one already exists (a developer's database
	// with a real signing key) the test skips rather than touch it.
	var existing int
	require.NoError(t, db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM instance_secrets").Scan(&existing))
	if existing > 0 {
		t.Skip("instance_secrets already holds a row; not deleting a key this test did not create")
	}
	defer func() {
		_, err := db.SQL.ExecContext(ctx, "DELETE FROM instance_secrets WHERE name = 'jwt_signing'")
		require.NoError(t, err)
	}()

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

// The startup path (#454): main.go calls auth.ResolveJWTSecret with the real
// store and uses the result as cfg.JWTSecret. A token issued before a restart
// must still verify after it, and must not verify under any other key.
func TestResolveJWTSecret_AgainstRealStore_SurvivesRestart(t *testing.T) {
	db, cleanup := testutil.NewDB(t)
	defer cleanup()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	store := database.NewSecretStore(q)
	ctx := context.Background()
	_, err := tx.ExecContext(ctx, "DELETE FROM instance_secrets")
	require.NoError(t, err)

	first, source, err := auth.ResolveJWTSecret(ctx, "", store, rand.Reader)
	require.NoError(t, err)
	require.Equal(t, "database", source)

	client := auth.OAuthClient{ID: uuid.New(), ClientID: "ci", Scopes: []string{"tickets:read"}}
	token, err := auth.IssueAccessToken(client, first)
	require.NoError(t, err)

	// "Restart": a short env value is ignored and the stored key comes back.
	second, source, err := auth.ResolveJWTSecret(ctx, "short", store, rand.Reader)
	require.NoError(t, err)
	require.Equal(t, "database", source)
	require.Equal(t, first, second)

	claims, err := auth.VerifyAccessToken(token, second)
	require.NoError(t, err)
	require.Equal(t, "ci", claims.ClientID)

	_, err = auth.VerifyAccessToken(token, "some-other-key-that-is-long-enough-xx")
	require.Error(t, err)

	// A long env value wins and the stored key is not consulted.
	long := "an-environment-secret-of-at-least-32-bytes"
	got, source, err := auth.ResolveJWTSecret(ctx, long, store, rand.Reader)
	require.NoError(t, err)
	require.Equal(t, "environment", source)
	require.Equal(t, long, got)
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
	secret := []byte("this-is-exactly-32-bytes-long!!!")
	err := store.InsertIfAbsent(ctx, "unknown_secret", secret)
	require.Error(t, err, "expected CHECK constraint to reject unknown name")
}
