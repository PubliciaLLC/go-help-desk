package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const (
	migration057Up   = "migrations/000057_email_template_overrides.up.sql"
	migration057Down = "migrations/000057_email_template_overrides.down.sql"
)

// Migration 000057 adds the table of edited email templates (#158). The test
// runs the down file and then the up file verbatim, inside a transaction that
// is rolled back.
//
// It has a database of its own because the down's DROP TABLE and the up's
// CREATE TABLE take an exclusive lock on users (the updated_by foreign key),
// and the test then writes to users. A test in another package that holds a
// users row and then reads the templates completes a lock cycle, and Postgres
// aborts one of the two with "deadlock detected". Same reason as 000041's down
// test (service_account_migration_down_test.go).
//
//   - down removes the table, up brings it back empty;
//   - a name is stored once;
//   - the two reserved shell names (_header, _footer) share the table;
//   - removing the user who last edited a row keeps the row and blanks the editor.
func TestMigration057_EmailTemplateOverrides(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	exists := func() bool {
		var ok bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT to_regclass('email_template_overrides') IS NOT NULL`).Scan(&ok))
		return ok
	}
	require.True(t, exists(), "the migration has run in the test database")

	execMigrationFile(t, ctx, tx, migration057Down, nil)
	require.False(t, exists(), "down drops the table")
	execMigrationFile(t, ctx, tx, migration057Up, nil)
	require.True(t, exists())

	var n int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM email_template_overrides`).Scan(&n))
	require.Zero(t, n, "starts empty, so every mail is the compiled default")

	var uid string
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users (email, display_name, role) VALUES ('m057@example.com', 'm', 'admin') RETURNING id`).Scan(&uid))
	for _, name := range []string{"ticket_created", "_header", "_footer"} {
		_, err = tx.ExecContext(ctx, `INSERT INTO email_template_overrides (name, subject, body, updated_by) VALUES ($1, 's', 'b', $2)`, name, uid)
		require.NoError(t, err, name)
	}

	_, err = tx.ExecContext(ctx, `SAVEPOINT s`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO email_template_overrides (name, subject, body) VALUES ('ticket_created', 'x', 'y')`)
	require.Error(t, err, "one row per template")
	_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT s`)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, uid)
	require.NoError(t, err)
	var by *string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT updated_by::text FROM email_template_overrides WHERE name = 'ticket_created'`).Scan(&by))
	require.Nil(t, by, "the text outlives the person who wrote it")
}
