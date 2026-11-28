package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const (
	migration059Up   = "migrations/000059_custom_field_retired_options.up.sql"
	migration059Down = "migrations/000059_custom_field_retired_options.down.sql"
)

// Migration 000059 adds custom_field_defs.retired_options (#603). Run verbatim
// in a rolled-back transaction, like 000058's test.
//
//   - down removes the column, up brings it back;
//   - a row saved before the column existed reads NULL ("none retired") and is
//     otherwise untouched, so upgrading changes nothing a field shows;
//   - the column holds JSON.
func TestMigration059_AddsRetiredOptionsWithoutTouchingRows(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	hasColumn := func() bool {
		var ok bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_name = 'custom_field_defs' AND column_name = 'retired_options' AND data_type = 'jsonb' AND is_nullable = 'YES')`).Scan(&ok))
		return ok
	}
	require.True(t, hasColumn(), "the migration has run in the test database")

	execMigrationFile(t, ctx, tx, migration059Down, nil)
	require.False(t, hasColumn(), "down removes the column")

	var id string
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO custom_field_defs (name, field_type, options, sort_order)
		VALUES ('zz_mig059', 'select', '["a","b"]', 7) RETURNING id`).Scan(&id), "a row saved under 000058")
	execMigrationFile(t, ctx, tx, migration059Up, nil)
	require.True(t, hasColumn())

	var retired *string
	var options string
	var sortOrder int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT retired_options::text, options::text, sort_order FROM custom_field_defs WHERE id = $1`, id).
		Scan(&retired, &options, &sortOrder))
	require.Nil(t, retired, "an old row has no retired options")
	require.JSONEq(t, `["a","b"]`, options)
	require.Equal(t, 7, sortOrder)

	_, err = tx.ExecContext(ctx, `UPDATE custom_field_defs SET retired_options = '["c"]' WHERE id = $1`, id)
	require.NoError(t, err)
}
