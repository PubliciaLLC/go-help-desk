package database_test

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Migration 000041's down path on the real tables (#502): the group_members
// cascade and the ticket_status_history nulling. The other down test runs on
// temp copies because the down's DDL takes exclusive locks on users and
// deadlocks neighbours in the shared test database. This one has a database of
// its own, so it can run against the real schema with the real foreign keys.
func TestMigration000041_DownOnRealTables_CascadesMembershipsAndKeepsHistory(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	mustExec := func(q string, args ...any) {
		t.Helper()
		_, err := tx.ExecContext(ctx, q, args...)
		require.NoError(t, err, q)
	}

	// An OAuth client with its service user, as 000041's up leaves them.
	svc, client, human := uuid.New(), uuid.New(), uuid.New()
	mustExec(`INSERT INTO users (id, email, display_name, role, service_account)
		VALUES ($1, 'svc@service-account.invalid', 'Nagios', 'staff', TRUE)`, svc)
	mustExec(`INSERT INTO oauth_clients (id, client_id, hashed_secret, name, scopes, user_id)
		VALUES ($1, 'c-nagios', 'x', 'Nagios', '{tickets:read}', $2)`, client, svc)
	mustExec(`INSERT INTO users (id, email, display_name, role, password_hash)
		VALUES ($1, 'human@test.local', 'Human', 'staff', 'hash')`, human)

	// Both users belong to a group: the service user's membership must go with
	// it, the person's must stay.
	group := uuid.New()
	mustExec(`INSERT INTO groups (id, name) VALUES ($1, 'Ops')`, group)
	mustExec(`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2), ($1, $3)`, group, svc, human)

	// A ticket whose status the service user changed, and one the person did.
	cat, ticketID := uuid.New(), uuid.New()
	mustExec(`INSERT INTO categories (id, name, sort_order, active) VALUES ($1, 'Cat', 1, TRUE)`, cat)
	var statusID uuid.UUID
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT id FROM statuses LIMIT 1`).Scan(&statusID))
	mustExec(`INSERT INTO tickets (id, tracking_number, subject, category_id, status_id, reporter_user_id, description_format, source_channel)
		VALUES ($1, 'T-502', 's', $2, $3, $4, 'plain', 'web')`, ticketID, cat, statusID, human)
	svcHist, humanHist := uuid.New(), uuid.New()
	mustExec(`INSERT INTO ticket_status_history (id, ticket_id, to_status_id, changed_by_user_id)
		VALUES ($1, $3, $4, $5), ($2, $3, $4, $6)`, svcHist, humanHist, ticketID, statusID, svc, human)

	// An audit row by the service user: audit_log.actor_id is ON DELETE SET NULL.
	auditID := uuid.New()
	mustExec(`INSERT INTO audit_log (id, actor_id, entity_type, entity_id, action)
		VALUES ($1, $2, 'ticket', $3, 'update')`, auditID, svc, ticketID)

	execMigrationFile(t, ctx, tx, "migrations/000041_oauth_client_service_users.down.sql", nil)

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		require.NoError(t, tx.QueryRowContext(ctx, q, args...).Scan(&n))
		return n
	}
	require.Zero(t, count(`SELECT count(*) FROM users WHERE id = $1`, svc), "the service user is gone")
	require.Equal(t, 1, count(`SELECT count(*) FROM oauth_clients WHERE id = $1`, client), "the client stays")

	require.Zero(t, count(`SELECT count(*) FROM group_members WHERE user_id = $1`, svc),
		"the service user's group membership went with it")
	require.Equal(t, 1, count(`SELECT count(*) FROM group_members WHERE user_id = $1`, human),
		"a person's membership is untouched")
	require.Equal(t, 1, count(`SELECT count(*) FROM groups WHERE id = $1`, group), "the group stays")

	require.Equal(t, 1, count(`SELECT count(*) FROM ticket_status_history WHERE id = $1 AND changed_by_user_id IS NULL`, svcHist),
		"the history row stays, with no user")
	require.Equal(t, 1, count(`SELECT count(*) FROM ticket_status_history WHERE id = $1 AND changed_by_user_id = $2`, humanHist, human),
		"a person's history row keeps its user")

	require.Equal(t, 1, count(`SELECT count(*) FROM audit_log WHERE id = $1 AND actor_id IS NULL`, auditID),
		"the audit row stays, with no actor")
}

// The rollback in docs/upgrade-notes-1.3.0.md runs every down file from the
// database's version back to 22. Copilot's review of #577 found that 000041's
// down failed on a ticket an OAuth client created with no reporter: the ticket
// has reporter_user_id = the client's service user and guest_email NULL, so
// deleting the service user sets reporter_user_id NULL (ON DELETE SET NULL)
// and breaks ticket_reporter_or_guest. This runs the whole chain, 58 down to
// 22, on a database holding data only 1.3.0 can create, so a down that cannot
// complete on such data fails here and not on somebody's production rollback.
func TestMigrationDownChain_CompletesWithOAuthCreatedTickets(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	mustExec := func(q string, args ...any) {
		t.Helper()
		_, err := tx.ExecContext(ctx, q, args...)
		require.NoError(t, err, q)
	}

	svc, client := uuid.New(), uuid.New()
	mustExec(`INSERT INTO users (id, email, display_name, role, service_account)
		VALUES ($1, 'oauth-x@service-account.invalid', 'Nagios', 'staff', TRUE)`, svc)
	mustExec(`INSERT INTO oauth_clients (id, client_id, hashed_secret, name, scopes, user_id)
		VALUES ($1, 'c-nagios', 'x', 'Nagios', '{tickets:create}', $2)`, client, svc)

	// What the API does for an OAuth client that names no reporter: the
	// client's own user is the reporter, and there is no guest.
	cat, ticketID := uuid.New(), uuid.New()
	mustExec(`INSERT INTO categories (id, name, sort_order, active) VALUES ($1, 'Cat', 1, TRUE)`, cat)
	var statusID uuid.UUID
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT id FROM statuses LIMIT 1`).Scan(&statusID))
	mustExec(`INSERT INTO tickets (id, tracking_number, subject, category_id, status_id, reporter_user_id, guest_email, description_format, source_channel)
		VALUES ($1, 'T-577', 'from nagios', $2, $3, $4, NULL, 'plain', 'api')`, ticketID, cat, statusID, svc)

	// Other 1.3.0-only data the rest of the chain has to get through.
	hook := uuid.New()
	mustExec(`INSERT INTO webhook_configs (id, url) VALUES ($1, 'https://example.invalid/h')`, hook)
	mustExec(`INSERT INTO webhook_deliveries (id, webhook_id, event_type, event, created_at, last_attempt_at, result, drop_reason)
		VALUES ($1, $2, 'ticket.created', '{}', now(), now(), 'dropped', 'filter')`, uuid.New(), hook)
	mustExec(`INSERT INTO pending_registrations (email, expires_at) VALUES ('p@test.local', now() + interval '1 day')`)

	files, err := filepath.Glob("migrations/*.down.sql")
	require.NoError(t, err)
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	ran := 0
	for _, f := range files {
		v := filepath.Base(f)[:6]
		if v > "000058" || v < "000022" {
			continue
		}
		// A failed statement aborts the transaction, so the first failure is
		// the only one that says anything: name the file that caused it.
		execMigrationFile(t, ctx, tx, f, nil)
		ran++
	}
	require.Greater(t, ran, 30, "the chain ran")

	var guest, guestName *string
	var reporter *uuid.UUID
	require.NoError(t, tx.QueryRowContext(ctx,
		`SELECT guest_email, guest_name, reporter_user_id FROM tickets WHERE id = $1`, ticketID).Scan(&guest, &guestName, &reporter),
		"the OAuth-created ticket survives the rollback")
	require.Nil(t, reporter, "the service user is gone")
	require.NotNil(t, guest, "the ticket still satisfies ticket_reporter_or_guest")
	require.Equal(t, "oauth-x@service-account.invalid", *guest,
		"the requester is kept as the client's own address, which names the client and can never receive mail")
	require.Equal(t, "Nagios", *guestName, "and under the client's name")
}
