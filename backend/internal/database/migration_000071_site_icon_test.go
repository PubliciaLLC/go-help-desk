package database_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The migration number is a placeholder (see the file names): it is assigned
// when this branch is merged, and this file and these two constants are renamed
// with it.
const (
	siteIconMigrationUp   = "migrations/000071_site_icon.up.sql"
	siteIconMigrationDown = "migrations/000071_site_icon.down.sql"
)

const sha64 = "0011223344556677889900112233445566778899001122334455667788990011"

// Migration for the site icon (#708). Every constraint the code relies on is
// exercised against the real tables: one row only, a lower-case #rrggbb colour,
// a 16-hex version, a source side between 512 and 4096, a digest of 64 hex, only
// the eight file names, files that go with their icon; and the down migration
// removes both tables and the up migration can run again after it.
//
// Run in a database of its own, in a transaction that is rolled back: the down
// migration locks the tables exclusively until it ends, and other tests read
// them.
func TestMigration_SiteIcon(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	refused := func(msg, stmt string, args ...any) {
		t.Helper()
		_, err := tx.ExecContext(ctx, `SAVEPOINT s`)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, stmt, args...)
		require.Error(t, err, msg)
		_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT s`)
		require.NoError(t, err)
	}
	count := func(table string) (n int) {
		t.Helper()
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n))
		return n
	}
	const insertIcon = `INSERT INTO site_icon (background, version, source_size, source_sha256) VALUES ($1, $2, $3, $4)`

	t.Run("empty after the migration", func(t *testing.T) {
		require.Zero(t, count("site_icon"))
		require.Zero(t, count("site_icon_files"))
	})

	t.Run("the metadata row", func(t *testing.T) {
		refused("a colour without the hash", insertIcon, "ffffff", "0123456789abcdef", 1024, sha64)
		refused("a short colour", insertIcon, "#FFF", "0123456789abcdef", 1024, sha64)
		refused("an upper-case colour is stored lower-case", insertIcon, "#FFFFFF", "0123456789abcdef", 1024, sha64)
		refused("a version of 15 characters", insertIcon, "#ffffff", "0123456789abcde", 1024, sha64)
		refused("a version that is not hex", insertIcon, "#ffffff", "0123456789abcdeg", 1024, sha64)
		refused("under 512", insertIcon, "#ffffff", "0123456789abcdef", 511, sha64)
		refused("over 4096", insertIcon, "#ffffff", "0123456789abcdef", 4097, sha64)
		refused("a digest of 63", insertIcon, "#ffffff", "0123456789abcdef", 1024, sha64[:63])

		_, err := tx.ExecContext(ctx, insertIcon, "#ffffff", "0123456789abcdef", 512, sha64)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `UPDATE site_icon SET source_size = 4096`)
		require.NoError(t, err, "both ends of the range are accepted")

		refused("a second row", `INSERT INTO site_icon (id, background, version, source_size, source_sha256)
			VALUES (FALSE, '#ffffff', '0123456789abcdef', 1024, $1)`, sha64)
		refused("a second row of the same id", insertIcon, "#000000", "fedcba9876543210", 1024, sha64)

		var updated sql.NullTime
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT updated_at FROM site_icon`).Scan(&updated))
		require.True(t, updated.Valid, "updated_at has a default")
	})

	t.Run("the files", func(t *testing.T) {
		for _, name := range []string{"x.png", "favicon-48.png", "icon-maskable-192.png", "../favicon.ico", ""} {
			refused("name "+name, `INSERT INTO site_icon_files (name, data) VALUES ($1, '\x00')`, name)
		}
		refused("no data", `INSERT INTO site_icon_files (name, data) VALUES ('master.png', NULL)`)
		for _, name := range []string{"master.png", "favicon.ico", "favicon-16.png", "favicon-32.png",
			"apple-touch-icon.png", "icon-192.png", "icon-512.png", "icon-maskable-512.png"} {
			_, err := tx.ExecContext(ctx, `INSERT INTO site_icon_files (name, data) VALUES ($1, '\x01')`, name)
			require.NoError(t, err, name)
		}
		refused("the same name twice", `INSERT INTO site_icon_files (name, data) VALUES ('master.png', '\x02')`)
		require.Equal(t, 8, count("site_icon_files"))

		_, err := tx.ExecContext(ctx, `DELETE FROM site_icon`)
		require.NoError(t, err)
		require.Zero(t, count("site_icon_files"), "deleting the icon deletes its files")
	})

	t.Run("a file without an icon is refused", func(t *testing.T) {
		refused("no icon row", `INSERT INTO site_icon_files (name, data) VALUES ('master.png', '\x01')`)
	})

	t.Run("down, and up again", func(t *testing.T) {
		_, err := tx.ExecContext(ctx, insertIcon, "#ffffff", "0123456789abcdef", 1024, sha64)
		require.NoError(t, err)

		execMigrationFile(t, ctx, tx, siteIconMigrationDown, nil)
		for _, table := range []string{"site_icon", "site_icon_files"} {
			var exists bool
			require.NoError(t, tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
			require.False(t, exists, "%s is gone", table)
		}

		execMigrationFile(t, ctx, tx, siteIconMigrationUp, nil)
		require.Zero(t, count("site_icon"), "up again starts empty")
		require.Zero(t, count("site_icon_files"))
		_, err = tx.ExecContext(ctx, insertIcon, "#abcdef", "fedcba9876543210", 600, strings.ToLower(sha64))
		require.NoError(t, err, "the tables work again")
	})
}
