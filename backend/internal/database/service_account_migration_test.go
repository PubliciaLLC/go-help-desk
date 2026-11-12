package database_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// preMigrationTables puts the two tables migration 000041 changes into the
// shape they had before it, as TEMP tables that shadow the real ones for this
// transaction. The migration then runs verbatim, every statement, against them.
//
// Not against the real tables: its ALTER TABLEs take exclusive locks, and
// holding those on users inside a shared test database made unrelated tests
// that touch users and oauth_clients deadlock with this one.
func preMigrationTables(t *testing.T, ctx context.Context, tx *sql.Tx) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TEMP TABLE users (LIKE public.users INCLUDING DEFAULTS) ON COMMIT DROP`,
		`ALTER TABLE pg_temp.users DROP COLUMN service_account`,
		`ALTER TABLE pg_temp.users ADD PRIMARY KEY (id)`,
		`ALTER TABLE pg_temp.users ADD UNIQUE (email)`,
		`CREATE TEMP TABLE oauth_clients (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			client_id TEXT NOT NULL UNIQUE,
			hashed_secret TEXT NOT NULL,
			name TEXT NOT NULL,
			scopes TEXT[] NOT NULL DEFAULT '{}',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()) ON COMMIT DROP`,
	} {
		_, err := tx.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
}

// Migration 000041 gives every existing OAuth client a service user (#420).
func TestMigration000041_BackfillsAServiceUserPerClient(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	preMigrationTables(t, ctx, tx)

	// A person whose display name equals a client's name: the backfill must
	// create a new user, never adopt this one.
	human := uuid.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, password_hash)
		VALUES ($1, $2, 'Zapier', 'staff', 'hash')`, human, "human-"+human.String()+"@test.local")
	require.NoError(t, err)

	var clients []uuid.UUID
	for _, name := range []string{"Zapier", "Nagios"} {
		id := uuid.New()
		_, err = tx.ExecContext(ctx, `INSERT INTO oauth_clients (id, client_id, hashed_secret, name, scopes)
			VALUES ($1, $2, 'x', $3, '{tickets:read}')`, id, "c-"+id.String(), name)
		require.NoError(t, err)
		clients = append(clients, id)
	}

	// The whole file, as the migrator runs it.
	execMigrationFile(t, ctx, tx, "migrations/000041_oauth_client_service_users.up.sql", nil)

	countServiceUsers := func() int {
		var n int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE service_account`).Scan(&n))
		return n
	}
	before := countServiceUsers()
	require.Equal(t, 2, before, "one per client")

	for _, id := range clients {
		var name, role, password, email string
		var service, mfa bool
		require.NoError(t, tx.QueryRowContext(ctx, `
			SELECT u.display_name, u.role, u.password_hash, u.email, u.service_account, u.mfa_enabled
			FROM oauth_clients c JOIN users u ON u.id = c.user_id WHERE c.id = $1`, id).
			Scan(&name, &role, &password, &email, &service, &mfa))
		require.True(t, service)
		require.Equal(t, "staff", role)
		require.Empty(t, password, "a service user has no password")
		require.False(t, mfa)
		require.Equal(t, "oauth-"+id.String()+"@service-account.invalid", email)
	}
	var zapier string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT u.display_name FROM oauth_clients c JOIN users u ON u.id = c.user_id WHERE c.id = $1`, clients[0]).Scan(&zapier))
	require.Equal(t, "Zapier", zapier, "named after the client")

	var humanUsers int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id = $1 AND NOT service_account AND password_hash = 'hash'`, human).Scan(&humanUsers))
	require.Equal(t, 1, humanUsers, "the person with the same name is untouched")

	// Running the backfill again must add nothing: every client has a user.
	execMigrationFile(t, ctx, tx, "migrations/000041_oauth_client_service_users.up.sql",
		func(stmt string) bool { return strings.HasPrefix(strings.ToUpper(stmt), "ALTER TABLE") })
	require.Equal(t, before, countServiceUsers())
}

// requireInertViolation asserts the write was refused by the CHECK this
// migration adds, and not by some other constraint that happens to fire.
func requireInertViolation(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23514", pgErr.Code, "a check violation")
	require.Equal(t, "users_service_account_inert", pgErr.ConstraintName)
}

// The down migration removes the service users and the columns, and leaves the
// clients themselves in place.
func TestMigration000041_DownRemovesTheUsersAndColumns(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	preMigrationTables(t, ctx, tx)

	clientID := uuid.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO oauth_clients (id, client_id, hashed_secret, name, scopes)
		VALUES ($1, $2, 'x', 'Nagios', '{tickets:read}')`, clientID, "c-"+clientID.String())
	require.NoError(t, err)
	execMigrationFile(t, ctx, tx, "migrations/000041_oauth_client_service_users.up.sql", nil)

	execMigrationFile(t, ctx, tx, "migrations/000041_oauth_client_service_users.down.sql", nil)

	var users, clients int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE email LIKE 'oauth-%'`).Scan(&users))
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM oauth_clients WHERE id = $1`, clientID).Scan(&clients))
	require.Zero(t, users, "the service user is gone")
	require.Equal(t, 1, clients, "the client stays")

	var cols int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema LIKE 'pg_temp%'
		  AND ((table_name = 'users' AND column_name = 'service_account')
		    OR (table_name = 'oauth_clients' AND column_name = 'user_id'))`).Scan(&cols))
	require.Zero(t, cols, "both columns are dropped")
}

// The inertness rule lives in the database, so no code path can give a service
// user a way to sign in, whatever it forgets to check.
func TestUsersServiceAccountInert_RefusesAWayInOrAdmin(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	cases := []struct {
		name  string
		role  string
		extra string // one column assignment that gives the row a way in
		ok    bool
	}{
		{"plain staff service user", "staff", "", true},
		{"plain reporting service user", "user", "", true},
		{"admin role", "admin", "", false},
		{"password", "staff", "password_hash = 'x'", false},
		{"TOTP secret", "staff", "mfa_secret = 'S'", false},
		{"TOTP enabled", "staff", "mfa_enabled = TRUE", false},
		{"SAML subject", "staff", "saml_subject = 's'", false},
		{"OIDC subject", "staff", "oidc_subject = 's'", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := db.SQL.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()

			id := uuid.New()
			_, err = tx.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, service_account)
				VALUES ($1, $2, 'svc', $3, TRUE)`, id, "svc-"+id.String()+"@service-account.invalid", tc.role)
			if tc.role == "admin" {
				requireInertViolation(t, err)
				return
			}
			require.NoError(t, err)
			if tc.extra == "" {
				return
			}
			// The same rule on a later write, not only on insert.
			_, err = tx.ExecContext(ctx, `UPDATE users SET `+tc.extra+` WHERE id = $1`, id)
			if tc.ok {
				require.NoError(t, err)
			} else {
				requireInertViolation(t, err)
			}
		})
	}

	t.Run("promotion to admin", func(t *testing.T) {
		tx, err := db.SQL.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		id := uuid.New()
		_, err = tx.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, service_account)
			VALUES ($1, $2, 'svc', 'staff', TRUE)`, id, "svc-"+id.String()+"@service-account.invalid")
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `UPDATE users SET role = 'admin' WHERE id = $1`, id)
		requireInertViolation(t, err)
	})
}
