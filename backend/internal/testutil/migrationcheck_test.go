package testutil

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
)

// testCtx bounds a test so that a regression in the advisory lock fails the
// test instead of hanging the package; CI runs without -timeout.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// freshDSN creates an empty database of the test's own, so the checksum table
// it writes cannot disturb, or be disturbed by, any other package's run.
func freshDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	name := "ghd_migcheck_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	admin, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		_ = admin.Close()
	})
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}

// withFile copies the real migrations and adds or replaces one file, standing
// in for another branch's migration set. Only the checksum pass reads it;
// golang-migrate always applies the real embedded files.
func withFile(t *testing.T, drop, name, body string) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	real := database.MigrationFiles()
	names, err := fs.Glob(real, "*.up.sql")
	require.NoError(t, err)
	for _, n := range names {
		if n == drop {
			continue
		}
		b, err := fs.ReadFile(real, n)
		require.NoError(t, err)
		m[n] = &fstest.MapFile{Data: b}
	}
	if name != "" {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func TestMigrateAndVerify(t *testing.T) {
	webauthn := "000029_webauthn_credentials.up.sql"
	cases := []struct {
		name    string
		second  func(t *testing.T) fs.FS
		wantErr []string
	}{
		{"the same tree again passes", func(t *testing.T) fs.FS { return database.MigrationFiles() }, nil},
		{"a recorded version this tree lacks is not a mismatch", func(t *testing.T) fs.FS {
			// A genuinely older checkout that no longer has this version would fail inside
			// golang-migrate with "no migration found", which the hint covers.
			return withFile(t, "", "000999_later.up.sql", "SELECT 1;")
		}, nil},
		{"another branch's migration under the same number is named", func(t *testing.T) fs.FS {
			return withFile(t, webauthn, "000029_saml_enabled_backfill.up.sql", "UPDATE settings SET value = value;")
		}, []string{"version 29", webauthn, "000029_saml_enabled_backfill.up.sql", "another branch or worktree"}},
		{"a migration edited after it was applied is named", func(t *testing.T) fs.FS {
			return withFile(t, webauthn, webauthn, "-- edited\n")
		}, []string{"version 29", webauthn, "edited after it was applied"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dsn := freshDSN(t)
			ctx := testCtx(t)
			pool, err := database.New(ctx, dsn)
			require.NoError(t, err)
			defer pool.Close()

			// The second set migrates first, as the other worktree did in #307;
			// this tree's real files then arrive at a database already "at 29".
			require.NoError(t, migrateAndVerify(ctx, pool, dsn, tc.second(t)))
			err = migrateAndVerify(ctx, pool, dsn, database.MigrationFiles())
			if tc.wantErr == nil {
				require.NoError(t, err)
				return
			}
			t.Logf("message:\n%v", err)
			require.Error(t, err)
			for _, s := range tc.wantErr {
				require.Contains(t, err.Error(), s)
			}
		})
	}
}

// A branch that adds a migration on top of an already-recorded database is
// the everyday case and must keep passing.
func TestMigrateAndVerify_AddingAMigrationPasses(t *testing.T) {
	dsn := freshDSN(t)
	ctx := testCtx(t)
	pool, err := database.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, migrateAndVerify(ctx, pool, dsn, database.MigrationFiles()))
	require.NoError(t, migrateAndVerify(ctx, pool, dsn, withFile(t, "", "000999_later.up.sql", "SELECT 1;")))
}

// Every package in `go test ./...` calls NewDB at once on a fresh database.
// Without the advisory lock the concurrent CREATE TABLE IF NOT EXISTS races.
func TestMigrateAndVerify_ConcurrentFirstUse(t *testing.T) {
	dsn := freshDSN(t)
	ctx := testCtx(t)
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool, err := database.New(ctx, dsn)
			if err != nil {
				errs <- err
				return
			}
			defer pool.Close()
			errs <- migrateAndVerify(ctx, pool, dsn, database.MigrationFiles())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

// The checksum pass must wait for whoever holds the lock: two processes that
// both see "version 29 not recorded" would otherwise race to record it.
func TestMigrateAndVerify_WaitsForTheLock(t *testing.T) {
	dsn := freshDSN(t)
	ctx := testCtx(t)
	pool, err := database.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	holder, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer holder.Release() // before pool.Close, which waits for it
	_, err = holder.Exec(ctx, `SELECT pg_advisory_lock(hashtext('ghd testutil migrations'))`)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- migrateAndVerify(ctx, pool, dsn, database.MigrationFiles()) }()
	select {
	case err := <-done:
		t.Fatalf("returned while another session held the lock (err=%v)", err)
	case <-time.After(2 * time.Second):
	}
	_, err = holder.Exec(ctx, `SELECT pg_advisory_unlock(hashtext('ghd testutil migrations'))`)
	require.NoError(t, err)
	require.NoError(t, <-done)
}

// When a checksum mismatch is detected, the database must not be migrated.
// golang-migrate always applies the real embedded files, so the only observable
// difference is whether it ran.
func TestMigrateAndVerify_MismatchLeavesDatabaseUntouched(t *testing.T) {
	dsn := freshDSN(t)
	ctx := testCtx(t)
	pool, err := database.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// Manually create the checksum table and insert a row for version 33 as if
	// another branch had recorded it.
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	_, err = conn.Exec(ctx, `CREATE TABLE testutil_migration_checksums (
		version BIGINT PRIMARY KEY, file TEXT NOT NULL, sha256 TEXT NOT NULL)`)
	require.NoError(t, err)

	// Compute the hash of "-- other branch\n"
	otherBody := []byte("-- other branch\n")
	sum := sha256.Sum256(otherBody)
	otherHash := hex.EncodeToString(sum[:])

	_, err = conn.Exec(ctx,
		`INSERT INTO testutil_migration_checksums (version, file, sha256) VALUES ($1, $2, $3)`,
		int64(33), "000033_pending_registrations_no_display_name.up.sql", otherHash)
	require.NoError(t, err)
	conn.Release()

	// Call migrateAndVerify with the real files: must error on version 33 mismatch.
	err = migrateAndVerify(ctx, pool, dsn, database.MigrationFiles())
	require.Error(t, err)
	require.Contains(t, err.Error(), "version 33")
	require.Contains(t, err.Error(), "000033_pending_registrations_no_display_name.up.sql")
	require.Contains(t, err.Error(), "another branch or worktree")

	// Assert database is untouched: schema_migrations and users tables must not exist.
	conn, err = pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	var schemaExists, usersExist bool
	err = conn.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&schemaExists)
	require.NoError(t, err)
	require.False(t, schemaExists, "schema_migrations should not exist after checksum mismatch")

	err = conn.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL`).Scan(&usersExist)
	require.NoError(t, err)
	require.False(t, usersExist, "users table should not exist after checksum mismatch")

	// Now call migrateAndVerify with fs that includes the "other branch" file.
	// This time it should pass.
	err = migrateAndVerify(ctx, pool, dsn, withFile(t, "", "000033_pending_registrations_no_display_name.up.sql", "-- other branch\n"))
	require.NoError(t, err)
}

// After migrateAndVerify completes, the advisory lock must be released.
func TestMigrateAndVerify_ReleasesTheLock(t *testing.T) {
	dsn := freshDSN(t)
	ctx := testCtx(t)
	pool, err := database.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// Run migrateAndVerify successfully.
	require.NoError(t, migrateAndVerify(ctx, pool, dsn, database.MigrationFiles()))

	// A second pool, so the probe runs in a different session: advisory locks
	// are re-entrant within one session, and a pooled connection could be the
	// very one that took the lock.
	other, err := database.New(ctx, dsn)
	require.NoError(t, err)
	defer other.Close()
	conn, err := other.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	var lockAcquired bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext('ghd testutil migrations'))`).Scan(&lockAcquired)
	require.NoError(t, err)
	require.True(t, lockAcquired, "lock should be available after migrateAndVerify returns")

	// Clean up.
	_, err = conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtext('ghd testutil migrations'))`)
	require.NoError(t, err)
}
