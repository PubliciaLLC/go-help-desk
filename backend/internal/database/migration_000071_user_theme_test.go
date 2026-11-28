package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const (
	migrationThemeUp   = "migrations/000071_user_theme.up.sql"
	migrationThemeDown = "migrations/000071_user_theme.down.sql"
)

// The user_theme migration (#709) runs here, verbatim, against a TEMP stand-in
// for users, as 000069 does for its tables: an ALTER TABLE on the real one holds
// an ACCESS EXCLUSIVE lock until the transaction ends and every other package
// reads it.
//
//   - an account that existed before reads as 'device', and no table is rewritten;
//   - an INSERT that does not name the column (CreateUser) gets 'device';
//   - the three values are accepted and anything else, NULL included, is refused;
//   - the down migration removes the column, and is harmless to repeat.
func TestMigrationTheme_UserTheme(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`CREATE TEMP TABLE users (id int, name text) ON COMMIT DROP`,
		`INSERT INTO users VALUES (1, 'existing')`,
	} {
		_, err := tx.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	filenode := func() uint32 {
		var n uint32
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT pg_relation_filenode('pg_temp.users'::regclass)`).Scan(&n))
		return n
	}
	before := filenode()

	execMigrationFile(t, ctx, tx, migrationThemeUp, nil)
	require.Equal(t, before, filenode(), "adding the column must not rewrite the table")

	var got string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT theme FROM users WHERE id = 1`).Scan(&got))
	require.Equal(t, "device", got, "an account that existed before follows the device")

	_, err = tx.ExecContext(ctx, `INSERT INTO users (id, name) VALUES (2, 'new')`)
	require.NoError(t, err, "an INSERT that does not name the column keeps working")
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT theme FROM users WHERE id = 2`).Scan(&got))
	require.Equal(t, "device", got)

	refused := func(stmt string, args ...any) {
		t.Helper()
		_, err := tx.ExecContext(ctx, `SAVEPOINT s`)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, stmt, args...)
		require.Error(t, err, stmt)
		_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT s`)
		require.NoError(t, err)
	}
	for i, v := range []string{"light", "dark", "device"} {
		_, err := tx.ExecContext(ctx, `INSERT INTO users (id, name, theme) VALUES ($1, 'x', $2)`, 10+i, v)
		require.NoError(t, err, v)
	}
	for _, v := range []string{"auto", "Dark", "system", ""} {
		refused(`INSERT INTO users (id, name, theme) VALUES (20, 'x', $1)`, v)
	}
	refused(`INSERT INTO users (id, name, theme) VALUES (21, 'x', NULL)`)
	refused(`UPDATE users SET theme = 'auto' WHERE id = 1`)

	execMigrationFile(t, ctx, tx, migrationThemeDown, nil)
	var n int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = (SELECT nspname FROM pg_namespace WHERE oid = pg_my_temp_schema())
		AND table_name = 'users' AND column_name = 'theme'`).Scan(&n))
	require.Zero(t, n, "the down migration removes the column")
	execMigrationFile(t, ctx, tx, migrationThemeDown, nil) // IF EXISTS: harmless on a database without it
}
