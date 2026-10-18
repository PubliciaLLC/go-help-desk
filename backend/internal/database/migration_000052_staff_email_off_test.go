package database_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const migration052 = "migrations/000052_staff_email_off_on_upgrade.up.sql"

var staffEmailOverrideKeys = []string{
	"staff_email_assigned_override",
	"staff_email_activity_override",
	"staff_email_group_override",
}

// shadow052 replaces users and settings with empty TEMP copies for this
// transaction, so the migration runs verbatim against exactly the data the test
// seeds and not whatever else the shared test database holds.
func shadow052(t *testing.T, ctx context.Context, tx *sql.Tx) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TEMP TABLE users (LIKE public.users INCLUDING DEFAULTS) ON COMMIT DROP`,
		`CREATE TEMP TABLE settings (LIKE public.settings INCLUDING ALL) ON COMMIT DROP`,
	} {
		_, err := tx.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
}

func seedUser052(t *testing.T, ctx context.Context, tx *sql.Tx, service bool) {
	t.Helper()
	id := uuid.New()
	pw := "hash"
	if service {
		pw = ""
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, password_hash, service_account)
		VALUES ($1, $2, 'u', 'staff', $3, $4)`, id, id.String()+"@test.local", pw, service)
	require.NoError(t, err)
}

func overrides052(t *testing.T, ctx context.Context, tx *sql.Tx) map[string]string {
	t.Helper()
	rows, err := tx.QueryContext(ctx, `SELECT key, value::text FROM settings WHERE key LIKE 'staff_email_%_override'`)
	require.NoError(t, err)
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var k, v string
		require.NoError(t, rows.Scan(&k, &v))
		got[k] = v
	}
	require.NoError(t, rows.Err())
	return got
}

// An install that already has people is an upgrade: staff email starts off.
func TestMigration052_UpgradedInstallStartsWithStaffEmailOff(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	shadow052(t, ctx, tx)
	seedUser052(t, ctx, tx, false)

	execMigrationFile(t, ctx, tx, migration052, nil)

	got := overrides052(t, ctx, tx)
	require.Len(t, got, 3)
	for _, k := range staffEmailOverrideKeys {
		require.Equal(t, `"off"`, got[k], k)
	}
}

// A fresh install has no users when it migrates (setup runs later), so the
// defaults apply. A service account alone is not an existing install either:
// migration 000041 makes those from OAuth clients, which a fresh one lacks, but
// the rule is "a person exists".
func TestMigration052_FreshInstallSetsNothing(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	shadow052(t, ctx, tx)

	execMigrationFile(t, ctx, tx, migration052, nil)
	require.Empty(t, overrides052(t, ctx, tx), "no users")

	seedUser052(t, ctx, tx, true)
	execMigrationFile(t, ctx, tx, migration052, nil)
	require.Empty(t, overrides052(t, ctx, tx), "only a service account")
}

// A value an administrator already stored wins; the keys they did not touch
// are still switched off.
func TestMigration052_KeepsAStoredOverride(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	shadow052(t, ctx, tx)
	seedUser052(t, ctx, tx, false)
	_, err = tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('staff_email_assigned_override', '"on"')`)
	require.NoError(t, err)

	execMigrationFile(t, ctx, tx, migration052, nil)

	got := overrides052(t, ctx, tx)
	require.Equal(t, `"on"`, got["staff_email_assigned_override"], "the stored choice is kept")
	require.Equal(t, `"off"`, got["staff_email_activity_override"])
	require.Equal(t, `"off"`, got["staff_email_group_override"])
}
