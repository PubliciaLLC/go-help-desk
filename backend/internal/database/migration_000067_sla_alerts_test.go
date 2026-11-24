package database_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const (
	migration067Up   = "migrations/000067_sla_alerts.up.sql"
	migration067Down = "migrations/000067_sla_alerts.down.sql"
)

// beginAs067Pre returns a transaction in which 000067 has been rolled back
// (its columns dropped, its settings rows left alone, as the down step does)
// and users and settings are empty TEMP copies. The up step then runs verbatim
// against exactly the data the test seeds, not whatever else the shared test
// database holds. The caller rolls it back in a defer registered after its
// closeDB defer: the pool cannot close while a transaction holds a connection.
func beginAs067Pre(t *testing.T, ctx context.Context, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	execMigrationFile(t, ctx, tx, migration067Down, nil)
	for _, stmt := range []string{
		`CREATE TEMP TABLE users (LIKE public.users INCLUDING DEFAULTS) ON COMMIT DROP`,
		`CREATE TEMP TABLE settings (LIKE public.settings INCLUDING ALL) ON COMMIT DROP`,
	} {
		_, err := tx.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	return tx
}

func seedPerson067(t *testing.T, ctx context.Context, tx *sql.Tx, service bool) {
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

func settingValue067(t *testing.T, ctx context.Context, tx *sql.Tx, key string) (string, bool) {
	t.Helper()
	var v string
	err := tx.QueryRowContext(ctx, `SELECT value::text FROM settings WHERE key = $1`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false
	}
	require.NoError(t, err)
	return v, true
}

func columnExists067(t *testing.T, ctx context.Context, tx *sql.Tx, table, column string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2)`,
		table, column).Scan(&ok))
	return ok
}

// A fresh install migrates with no people: sla_alerts_enabled is on, no staff
// email override is stored, and the per-person switch defaults to on.
func TestMigration067_FreshInstallDefaults(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx := beginAs067Pre(t, ctx, db.SQL)
	defer func() { _ = tx.Rollback() }()

	execMigrationFile(t, ctx, tx, migration067Up, nil)

	v, ok := settingValue067(t, ctx, tx, "sla_alerts_enabled")
	require.True(t, ok)
	require.Equal(t, "true", v, "no people: SLA alerts start on")

	_, ok = settingValue067(t, ctx, tx, "staff_email_sla_override")
	require.False(t, ok, "no people: no override is stored, so the default applies")

	seedPerson067(t, ctx, tx, false)
	var notify bool
	require.NoError(t, tx.QueryRowContext(ctx,
		`SELECT email_notify_sla FROM users LIMIT 1`).Scan(&notify))
	require.True(t, notify, "email_notify_sla defaults to true")
}

// An install that already has a person is an upgrade: SLA alerts start off and
// staff SLA mail is held back by an "off" override.
func TestMigration067_UpgradedInstallStartsOff(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx := beginAs067Pre(t, ctx, db.SQL)
	defer func() { _ = tx.Rollback() }()
	seedPerson067(t, ctx, tx, false)

	execMigrationFile(t, ctx, tx, migration067Up, nil)

	v, ok := settingValue067(t, ctx, tx, "sla_alerts_enabled")
	require.True(t, ok)
	require.Equal(t, "false", v, "an upgraded install starts with SLA alerts off")

	v, ok = settingValue067(t, ctx, tx, "staff_email_sla_override")
	require.True(t, ok)
	require.Equal(t, `"off"`, v)

	// The person who existed before the column did reads it as on: the
	// per-user switch is a preference, and only the admin override holds
	// SLA mail back on an upgrade.
	var notify bool
	require.NoError(t, tx.QueryRowContext(ctx,
		`SELECT email_notify_sla FROM users LIMIT 1`).Scan(&notify))
	require.True(t, notify, "an account that predates the column reads email_notify_sla = true")
}

// A service account is not a person, so it does not make an install an upgrade.
func TestMigration067_ServiceAccountAloneIsFresh(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx := beginAs067Pre(t, ctx, db.SQL)
	defer func() { _ = tx.Rollback() }()
	seedPerson067(t, ctx, tx, true)

	execMigrationFile(t, ctx, tx, migration067Up, nil)

	v, ok := settingValue067(t, ctx, tx, "sla_alerts_enabled")
	require.True(t, ok)
	require.Equal(t, "true", v)
	_, ok = settingValue067(t, ctx, tx, "staff_email_sla_override")
	require.False(t, ok)
}

// A value an administrator already stored wins, on either key.
func TestMigration067_StoredValuesWin(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx := beginAs067Pre(t, ctx, db.SQL)
	defer func() { _ = tx.Rollback() }()
	seedPerson067(t, ctx, tx, false)
	_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES
		('sla_alerts_enabled', 'true'),
		('staff_email_sla_override', '"on"')`)
	require.NoError(t, err)

	execMigrationFile(t, ctx, tx, migration067Up, nil)

	v, _ := settingValue067(t, ctx, tx, "sla_alerts_enabled")
	require.Equal(t, "true", v, "the stored choice is kept")
	v, _ = settingValue067(t, ctx, tx, "staff_email_sla_override")
	require.Equal(t, `"on"`, v, "the stored choice is kept")
}

// Down drops the three new columns (and only them); up brings them back with
// their defaults (up runs once after down; it is not run twice in a row). The
// settings rows are left alone by down, so an administrator's choice survives a
// round trip.
func TestMigration067_DownThenUpAgain(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	require.True(t, columnExists067(t, ctx, tx, "sla_records", "response_at_risk_at"), "the migration has run in the test database")
	require.True(t, columnExists067(t, ctx, tx, "users", "email_notify_sla"))

	_, err = tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('sla_alerts_enabled', 'true')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	require.NoError(t, err)

	execMigrationFile(t, ctx, tx, migration067Down, nil)
	require.False(t, columnExists067(t, ctx, tx, "sla_records", "response_at_risk_at"))
	require.False(t, columnExists067(t, ctx, tx, "sla_records", "resolution_at_risk_at"))
	require.False(t, columnExists067(t, ctx, tx, "users", "email_notify_sla"))
	require.True(t, columnExists067(t, ctx, tx, "users", "email_notify_assigned"), "down touches only 000067's columns")
	_, ok := settingValue067(t, ctx, tx, "sla_alerts_enabled")
	require.True(t, ok, "down is a no-op on settings")

	execMigrationFile(t, ctx, tx, migration067Up, nil)
	require.True(t, columnExists067(t, ctx, tx, "sla_records", "response_at_risk_at"))
	require.True(t, columnExists067(t, ctx, tx, "sla_records", "resolution_at_risk_at"))
	require.True(t, columnExists067(t, ctx, tx, "users", "email_notify_sla"))

	v, _ := settingValue067(t, ctx, tx, "sla_alerts_enabled")
	require.Equal(t, "true", v, "up does not overwrite a stored value")
}
