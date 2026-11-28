package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const (
	migration055Up   = "migrations/000055_text_format.up.sql"
	migration055Down = "migrations/000055_text_format.down.sql"
)

// Migration 000055 adds the format columns (#443). It runs here against TEMP
// stand-ins for the two tables, verbatim, so the test sees exactly the rows it
// seeds and takes no lock on the shared tables.
//
//   - no table rewrite (the filenode survives);
//   - rows that were already there read 'plain';
//   - the default is gone afterwards, so an INSERT that forgets the format fails;
//   - the CHECK refuses any other value;
//   - the down migration removes the columns again.
func TestMigration055_TextFormat(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`CREATE TEMP TABLE tickets (id int, description text) ON COMMIT DROP`,
		`CREATE TEMP TABLE ticket_replies (id int, body text) ON COMMIT DROP`,
		`INSERT INTO tickets VALUES (1, 'old')`,
		`INSERT INTO ticket_replies VALUES (1, 'old')`,
	} {
		_, err := tx.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	filenode := func(table string) uint32 {
		var n uint32
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT pg_relation_filenode($1::regclass)`, "pg_temp."+table).Scan(&n))
		return n
	}
	tBefore, rBefore := filenode("tickets"), filenode("ticket_replies")

	execMigrationFile(t, ctx, tx, migration055Up, nil)

	require.Equal(t, tBefore, filenode("tickets"), "adding the column must not rewrite tickets")
	require.Equal(t, rBefore, filenode("ticket_replies"), "adding the column must not rewrite ticket_replies")

	var df, bf string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT description_format FROM tickets WHERE id = 1`).Scan(&df))
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT body_format FROM ticket_replies WHERE id = 1`).Scan(&bf))
	require.Equal(t, "plain", df, "text written before 1.3.0 is plain")
	require.Equal(t, "plain", bf)

	// Each attempt in a savepoint: a failed statement aborts the transaction.
	refused := func(stmt string) {
		t.Helper()
		_, err := tx.ExecContext(ctx, `SAVEPOINT s`)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, stmt)
		require.Error(t, err, stmt)
		_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT s`)
		require.NoError(t, err)
	}
	refused(`INSERT INTO tickets (id, description) VALUES (2, 'x')`)
	refused(`INSERT INTO ticket_replies (id, body) VALUES (2, 'x')`)
	refused(`INSERT INTO tickets (id, description, description_format) VALUES (2, 'x', 'html')`)
	refused(`INSERT INTO ticket_replies (id, body, body_format) VALUES (2, 'x', 'Markdown')`)
	_, err = tx.ExecContext(ctx, `INSERT INTO tickets VALUES (2, 'x', 'markdown')`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO ticket_replies VALUES (2, 'x', 'markdown')`)
	require.NoError(t, err)

	execMigrationFile(t, ctx, tx, migration055Down, nil)
	var n int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = (SELECT nspname FROM pg_namespace WHERE oid = pg_my_temp_schema())
		AND column_name IN ('description_format', 'body_format')`).Scan(&n))
	require.Zero(t, n, "the down migration removes both columns")
}

// The real tables, after every migration has run: NOT NULL and no default.
func TestMigration055_RealTablesHaveNoDefault(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	for _, c := range []struct{ table, col string }{
		{"tickets", "description_format"},
		{"ticket_replies", "body_format"},
	} {
		var nullable string
		var def *string
		require.NoError(t, db.SQL.QueryRow(`SELECT is_nullable, column_default FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`, c.table, c.col).Scan(&nullable, &def))
		require.Equal(t, "NO", nullable, c.col)
		require.Nil(t, def, "%s must have no default: a write path that forgets the format has to fail", c.col)
	}
}
