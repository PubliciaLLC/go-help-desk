package database_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Migration 000041 gives every existing OAuth client a service user (#420).
//
// The test database is already migrated, so oauth_clients.user_id is NOT NULL.
// To stand in the position of an instance that is upgrading, the test drops
// that one constraint inside its transaction, seeds clients with no user, and
// runs the migration's backfill statement verbatim from disk (skipping the
// ALTER TABLEs, whose effects the schema already has).
func TestMigration000041_BackfillsAServiceUserPerClient(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `ALTER TABLE oauth_clients ALTER COLUMN user_id DROP NOT NULL`)
	require.NoError(t, err)

	// A person whose display name equals a client's name: the backfill must
	// create a new user, never adopt this one.
	human := uuid.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, password_hash)
		VALUES ($1, $2, 'Zapier', 'staff', 'hash')`, human, "human-"+human.String()+"@test.local")
	require.NoError(t, err)

	type seeded struct{ id uuid.UUID }
	var clients []seeded
	for _, name := range []string{"Zapier", "Nagios"} {
		id := uuid.New()
		_, err = tx.ExecContext(ctx, `INSERT INTO oauth_clients (id, client_id, hashed_secret, name, scopes)
			VALUES ($1, $2, 'x', $3, '{tickets:read}')`, id, "c-"+id.String(), name)
		require.NoError(t, err)
		clients = append(clients, seeded{id})
	}

	runBackfill := func() {
		execMigrationFile(t, ctx, tx, "migrations/000041_oauth_client_service_users.up.sql",
			func(stmt string) bool { return strings.HasPrefix(strings.ToUpper(stmt), "ALTER TABLE") })
	}
	runBackfill()

	countServiceUsers := func() int {
		var n int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE service_account`).Scan(&n))
		return n
	}
	before := countServiceUsers()

	for _, c := range clients {
		var name, role, password, email string
		var service bool
		var mfa bool
		var groups int
		require.NoError(t, tx.QueryRowContext(ctx, `
			SELECT u.display_name, u.role, u.password_hash, u.email, u.service_account, u.mfa_enabled,
			       (SELECT count(*) FROM group_members gm WHERE gm.user_id = u.id)
			FROM oauth_clients c JOIN users u ON u.id = c.user_id WHERE c.id = $1`, c.id).
			Scan(&name, &role, &password, &email, &service, &mfa, &groups))
		require.True(t, service)
		require.Equal(t, "staff", role)
		require.Empty(t, password, "a service user has no password")
		require.False(t, mfa)
		require.Zero(t, groups)
		require.Equal(t, "oauth-"+c.id.String()+"@service-account.invalid", email)
	}
	var zapier string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT u.display_name FROM oauth_clients c JOIN users u ON u.id = c.user_id WHERE c.id = $1`, clients[0].id).Scan(&zapier))
	require.Equal(t, "Zapier", zapier, "named after the client")

	var humanUsers int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id = $1 AND NOT service_account AND password_hash = 'hash'`, human).Scan(&humanUsers))
	require.Equal(t, 1, humanUsers, "the person with the same name is untouched")

	// Running it again must add nothing: every client already has a user.
	runBackfill()
	require.Equal(t, before, countServiceUsers())
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

	userID, clientID := uuid.New(), uuid.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, service_account)
		VALUES ($1, $2, 'Nagios', 'staff', TRUE)`, userID, "oauth-"+clientID.String()+"@service-account.invalid")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO oauth_clients (id, client_id, hashed_secret, name, scopes, user_id)
		VALUES ($1, $2, 'x', 'Nagios', '{tickets:read}', $3)`, clientID, "c-"+clientID.String(), userID)
	require.NoError(t, err)
	g := uuid.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO groups (id, name, description) VALUES ($1, $2, '')`, g, "down-"+g.String())
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, g, userID)
	require.NoError(t, err)

	execMigrationFile(t, ctx, tx, "migrations/000041_oauth_client_service_users.down.sql", nil)

	var users, clients int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id = $1`, userID).Scan(&users))
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM oauth_clients WHERE id = $1`, clientID).Scan(&clients))
	require.Zero(t, users, "the service user is gone")
	require.Equal(t, 1, clients, "the client stays")

	var cols int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE (table_name = 'users' AND column_name = 'service_account')
		   OR (table_name = 'oauth_clients' AND column_name = 'user_id')`).Scan(&cols))
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
				require.Error(t, err, "insert as admin must be refused")
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
				require.Error(t, err, "%s must be refused on a service user", tc.name)
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
		require.Error(t, err)
	})
}
