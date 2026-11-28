package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const (
	migration063Up   = "migrations/000063_rate_limit_counters.up.sql"
	migration063Down = "migrations/000063_rate_limit_counters.down.sql"
)

// Migration 000063 adds the rate-limit counters (#621). Down then up, verbatim,
// inside a transaction that is rolled back.
//
//   - down drops the table, up brings it back empty;
//   - a key is stored once, and a count cannot go negative;
//   - the sweep's index on window_end exists.
func TestMigration063_RateLimitCounters(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	exists := func() bool {
		var ok bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT to_regclass('rate_limit_counters') IS NOT NULL`).Scan(&ok))
		return ok
	}
	require.True(t, exists(), "the migration has run in the test database")

	execMigrationFile(t, ctx, tx, migration063Down, nil)
	require.False(t, exists(), "down drops the table")
	execMigrationFile(t, ctx, tx, migration063Up, nil)
	require.True(t, exists())

	var n int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM rate_limit_counters`).Scan(&n))
	require.Zero(t, n, "starts empty: every key has its whole budget")

	var idx bool
	require.NoError(t, tx.QueryRowContext(ctx,
		`SELECT to_regclass('rate_limit_counters_window_end_idx') IS NOT NULL`).Scan(&idx))
	require.True(t, idx, "the sweep deletes by window_end and needs the index")

	_, err = tx.ExecContext(ctx, `INSERT INTO rate_limit_counters (key, window_start, window_end, count) VALUES ('k', now(), now(), 1)`)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, `SAVEPOINT s`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO rate_limit_counters (key, window_start, window_end, count) VALUES ('k', now(), now(), 1)`)
	require.Error(t, err, "one row per key")
	_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT s`)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, `UPDATE rate_limit_counters SET count = -1 WHERE key = 'k'`)
	require.Error(t, err, "a count below zero is refused")
}
