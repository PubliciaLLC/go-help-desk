package database_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const (
	migration069Up   = "migrations/000069_cti_description.up.sql"
	migration069Down = "migrations/000069_cti_description.down.sql"
)

// Migration 000069 adds description to categories, types and items. It runs
// here, verbatim, against TEMP stand-ins for the three tables, as 000056 does for
// tickets: the real tables are read by every other test, and an ALTER TABLE on
// one holds an ACCESS EXCLUSIVE lock until the transaction ends.
//
//   - rows that were already there read as an empty string (none), no table rewritten;
//   - the column is NOT NULL, so "none" has one spelling;
//   - 500 characters are accepted and 501 are refused, counted in characters
//     (a four-byte character is one), by the CHECK, which backs up the service;
//   - the down migration removes the column again, and is harmless to repeat.
func TestMigration069_CTIDescription(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	tables := []string{"categories", "types", "items"}
	for _, tbl := range tables {
		for _, stmt := range []string{
			`CREATE TEMP TABLE ` + tbl + ` (id int, name text) ON COMMIT DROP`,
			`INSERT INTO ` + tbl + ` VALUES (1, 'existing')`,
		} {
			_, err := tx.ExecContext(ctx, stmt)
			require.NoError(t, err)
		}
	}
	filenode := func(tbl string) uint32 {
		var n uint32
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT pg_relation_filenode('pg_temp.`+tbl+`'::regclass)`).Scan(&n))
		return n
	}
	before := map[string]uint32{}
	for _, tbl := range tables {
		before[tbl] = filenode(tbl)
	}

	execMigrationFile(t, ctx, tx, migration069Up, nil)

	refused := func(stmt string, args ...any) {
		t.Helper()
		_, err := tx.ExecContext(ctx, `SAVEPOINT s`)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, stmt, args...)
		require.Error(t, err, stmt)
		_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT s`)
		require.NoError(t, err)
	}
	for _, tbl := range tables {
		require.Equal(t, before[tbl], filenode(tbl), "adding the column must not rewrite %s", tbl)

		var d string
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT description FROM `+tbl+` WHERE id = 1`).Scan(&d))
		require.Equal(t, "", d, "a row that existed before reads as having none")

		_, err = tx.ExecContext(ctx, `INSERT INTO `+tbl+` (id, name) VALUES (2, 'no description given')`)
		require.NoError(t, err, "the column has a default, so existing INSERTs keep working")

		refused(`INSERT INTO ` + tbl + ` (id, name, description) VALUES (3, 'x', NULL)`)
		_, err = tx.ExecContext(ctx, `INSERT INTO `+tbl+` (id, name, description) VALUES (4, 'x', $1)`, strings.Repeat("a", 500))
		require.NoError(t, err, "500 characters")
		refused(`INSERT INTO `+tbl+` (id, name, description) VALUES (5, 'x', $1)`, strings.Repeat("a", 501))
		_, err = tx.ExecContext(ctx, `INSERT INTO `+tbl+` (id, name, description) VALUES (6, 'x', $1)`, strings.Repeat("😀", 500))
		require.NoError(t, err, "500 four-byte characters are 500 characters")
		refused(`INSERT INTO `+tbl+` (id, name, description) VALUES (7, 'x', $1)`, strings.Repeat("😀", 501))
	}

	execMigrationFile(t, ctx, tx, migration069Down, nil)
	for _, tbl := range tables {
		var n int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema = (SELECT nspname FROM pg_namespace WHERE oid = pg_my_temp_schema())
			AND table_name = $1 AND column_name = 'description'`, tbl).Scan(&n))
		require.Zero(t, n, "the down migration removes the column from %s", tbl)
	}
	execMigrationFile(t, ctx, tx, migration069Down, nil) // IF EXISTS: harmless on a database without it
}
