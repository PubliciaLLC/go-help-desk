package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const (
	migration066Up   = "migrations/000066_tickets_done_at_index.up.sql"
	migration066Down = "migrations/000066_tickets_done_at_index.down.sql"
)

// Migration 000066 adds the index the dashboard's volume trend reads its
// resolved arm through (#675). Down, up, up again, verbatim, inside a
// transaction that is rolled back.
func TestMigration066_TicketsDoneAtIndex(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	exists := func() bool {
		var ok bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT to_regclass('tickets_done_at_idx') IS NOT NULL`).Scan(&ok))
		return ok
	}
	require.True(t, exists(), "the migration has run in the test database")

	execMigrationFile(t, ctx, tx, migration066Down, nil)
	require.False(t, exists(), "down drops the index")
	execMigrationFile(t, ctx, tx, migration066Down, nil) // IF EXISTS: harmless on a database without it

	execMigrationFile(t, ctx, tx, migration066Up, nil)
	require.True(t, exists(), "up creates the index")
	execMigrationFile(t, ctx, tx, migration066Up, nil) // IF NOT EXISTS: harmless on a database that has it
	require.True(t, exists())

	// The index is on the expression the trend filters by, so the planner can
	// match it: COALESCE(resolved_at, closed_at).
	var def string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname = 'tickets_done_at_idx'`).Scan(&def))
	require.Contains(t, def, "COALESCE(resolved_at, closed_at)")
}
