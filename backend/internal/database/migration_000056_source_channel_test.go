package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const (
	migration056Up   = "migrations/000056_ticket_source_channel.up.sql"
	migration056Down = "migrations/000056_ticket_source_channel.down.sql"
)

// Migration 000056 adds tickets.source_channel (#130). It runs here against a
// TEMP stand-in for tickets, verbatim:
//
//   - no table rewrite (the filenode survives);
//   - rows that were already there read 'web' (Erik's ruling: it invents
//     history for tickets that came in by API or MCP, and the 1.3.0 upgrade
//     notes say so);
//   - the default is gone afterwards, so an INSERT that forgets the channel fails;
//   - the CHECK accepts the five values and refuses anything else, 'email'
//     included (reserved: adding it later is a migration);
//   - the down migration removes the column again.
func TestMigration056_SourceChannel(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`CREATE TEMP TABLE tickets (id int) ON COMMIT DROP`,
		`INSERT INTO tickets VALUES (1)`,
	} {
		_, err := tx.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	var before uint32
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT pg_relation_filenode('pg_temp.tickets'::regclass)`).Scan(&before))

	execMigrationFile(t, ctx, tx, migration056Up, nil)

	var after uint32
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT pg_relation_filenode('pg_temp.tickets'::regclass)`).Scan(&after))
	require.Equal(t, before, after, "adding the column must not rewrite tickets")

	var ch string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT source_channel FROM tickets WHERE id = 1`).Scan(&ch))
	require.Equal(t, "web", ch, "tickets that existed before 1.3.0 read web")

	refused := func(stmt string) {
		t.Helper()
		_, err := tx.ExecContext(ctx, `SAVEPOINT s`)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, stmt)
		require.Error(t, err, stmt)
		_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT s`)
		require.NoError(t, err)
	}
	refused(`INSERT INTO tickets (id) VALUES (2)`)
	for i, bad := range []string{"email", "WEB", "", "unknown", "sms"} {
		refused(`INSERT INTO tickets (id, source_channel) VALUES (` + string(rune('3'+i)) + `, '` + bad + `')`)
	}
	for i, ok := range []string{"web", "api", "mcp", "guest", "phone"} {
		_, err = tx.ExecContext(ctx, `INSERT INTO tickets (id, source_channel) VALUES ($1, $2)`, 10+i, ok)
		require.NoError(t, err, ok)
	}

	execMigrationFile(t, ctx, tx, migration056Down, nil)
	var n int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = (SELECT nspname FROM pg_namespace WHERE oid = pg_my_temp_schema())
		AND column_name = 'source_channel'`).Scan(&n))
	require.Zero(t, n, "the down migration removes the column")
}

// The real table, after every migration has run: NOT NULL, no default, and the
// CHECK in place.
func TestMigration056_RealTableHasNoDefaultAndRefusesUnknown(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	var nullable string
	var def *string
	require.NoError(t, db.SQL.QueryRow(`SELECT is_nullable, column_default FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'tickets' AND column_name = 'source_channel'`).Scan(&nullable, &def))
	require.Equal(t, "NO", nullable)
	require.Nil(t, def, "no default: a write path that forgets the channel has to fail, not be recorded as web")

	var n int
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM pg_constraint
		WHERE conrelid = 'public.tickets'::regclass AND contype = 'c'
		AND pg_get_constraintdef(oid) LIKE '%source_channel%'`).Scan(&n))
	require.Equal(t, 1, n, "the CHECK constraint on source_channel exists")
}
