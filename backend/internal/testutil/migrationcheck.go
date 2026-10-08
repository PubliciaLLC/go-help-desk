package testutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
)

// migrateAndVerify runs the migrations, then checks that every version the
// database has applied was applied from the same file this tree carries.
//
// golang-migrate records only a version number. Two branches that each add a
// migration 29 both see "version 29, nothing to do" on a database the other
// one migrated, and the second one's tests then fail on a missing table with
// nothing pointing at the cause (#307). A checksum per version, kept in the
// test database, turns that into an error that names it.
//
// The advisory lock serialises whole migrate-and-record sequences across the
// packages `go test ./...` runs in parallel, so a process never verifies
// against a database another one is half-way through recording.
func migrateAndVerify(ctx context.Context, pool *pgxpool.Pool, dsn string, files fs.FS) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('ghd testutil migrations'))`); err != nil {
		return fmt.Errorf("taking migration lock: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('ghd testutil migrations'))`) //nolint:errcheck

	if err := database.Migrate(ctx, database.MigrateURL(dsn)); err != nil {
		return fmt.Errorf("migrate: %w%s", err, hint)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS testutil_migration_checksums (
		version BIGINT PRIMARY KEY, file TEXT NOT NULL, sha256 TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("creating checksum table: %w", err)
	}

	// One read and at most one write: NewDB runs once per test, ~110 times in
	// internal/server alone, and every call holds the lock.
	recorded := map[int64][2]string{} // version -> file, sha256
	rows, err := conn.Query(ctx, `SELECT version, file, sha256 FROM testutil_migration_checksums`)
	if err != nil {
		return fmt.Errorf("reading checksums: %w", err)
	}
	for rows.Next() {
		var v int64
		var f, h string
		if err := rows.Scan(&v, &f, &h); err != nil {
			return fmt.Errorf("reading checksums: %w", err)
		}
		recorded[v] = [2]string{f, h}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading checksums: %w", err)
	}

	names, err := fs.Glob(files, "*.up.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	var mismatches []string
	var newV []int64
	var newF, newH []string
	for _, name := range names {
		v, err := strconv.ParseInt(strings.SplitN(name, "_", 2)[0], 10, 64)
		if err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		body, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		mine := hex.EncodeToString(sum[:])
		theirs, ok := recorded[v]
		switch {
		case !ok: // first tree to see this version records it
			newV, newF, newH = append(newV, v), append(newF, name), append(newH, mine)
		case theirs[1] != mine:
			mismatches = append(mismatches, fmt.Sprintf("  version %d: the database has %s (sha256 %.12s), this tree has %s (sha256 %.12s)",
				v, theirs[0], theirs[1], name, mine))
		}
	}
	if len(newV) > 0 {
		if _, err := conn.Exec(ctx, `INSERT INTO testutil_migration_checksums (version, file, sha256)
			SELECT * FROM unnest($1::bigint[], $2::text[], $3::text[])`, newV, newF, newH); err != nil {
			return fmt.Errorf("recording checksums: %w", err)
		}
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("the test database was migrated by different migration files than this tree's:\n%s\n"+
			"golang-migrate records only the version number, so it treats the database as up to date "+
			"and the schema these tests expect is not there.%s", strings.Join(mismatches, "\n"), hint)
	}
	return nil
}

const hint = "\nUsual cause: another branch or worktree ran the suite against the same database, " +
	"or a migration was edited after it was applied. Recreate the test database: " +
	"./scripts/test-db.sh down (with the same GHD_TEST_INSTANCE, if you set one), " +
	"or point TEST_DATABASE_URL at a database of your own."
