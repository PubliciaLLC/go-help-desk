package database_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Migration 000053 returns the open tickets of users deleted under 1.2.x to
// the queue, the way 1.3.0's own delete does (#524). 1.2.x soft-deleted the
// user and left the ticket pointing at them.
func TestMigration053_UnassignsOpenTicketsHeldByDeletedUsers(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)
	us, cs, ts := userstore.New(q), categorystore.New(q), ticketstore.New(q)

	mkUser := func(role user.Role) uuid.UUID {
		u := user.User{
			ID: uuid.New(), Email: "m53-" + uuid.NewString() + "@test.local", DisplayName: "M53",
			Role: role, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		require.NoError(t, us.Create(ctx, u))
		return u.ID
	}
	reporter := mkUser(user.RoleUser)
	gone := mkUser(user.RoleStaff)
	goneToo := mkUser(user.RoleStaff)
	disabled := mkUser(user.RoleStaff)
	live := mkUser(user.RoleStaff)
	_, err = tx.ExecContext(ctx, `UPDATE users SET deleted_at = now() WHERE id IN ($1, $2)`, gone, goneToo)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE users SET disabled = true WHERE id = $1`, disabled)
	require.NoError(t, err)

	cat := category.Category{ID: uuid.New(), Name: "M53 " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	mkTicket := func(assignee *uuid.UUID, resolved, closed bool) uuid.UUID {
		id := uuid.New()
		require.NoError(t, ts.Create(ctx, ticket.Ticket{DescriptionFormat: ticket.TextFormatPlain,
			ID: id, TrackingNumber: ticket.TrackingNumber("M53-" + uuid.NewString()[:8]),
			Subject: "m53", Description: "m53", CategoryID: cat.ID, Priority: ticket.PriorityMedium,
			StatusID: newSt.ID, ReporterUserID: &reporter, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}))
		_, err := tx.ExecContext(ctx, `UPDATE tickets SET assignee_user_id = $2,
			resolved_at = CASE WHEN $3 THEN now() END, closed_at = CASE WHEN $4 THEN now() END WHERE id = $1`,
			id, assignee, resolved, closed)
		require.NoError(t, err)
		return id
	}
	heldByGone := mkTicket(&gone, false, false)
	heldByGoneToo := mkTicket(&goneToo, false, false)
	resolvedHeldByGone := mkTicket(&gone, true, false)
	closedHeldByGone := mkTicket(&gone, true, true)
	// closed_at without resolved_at exists on 1.2-shaped data; it is still
	// closed, so still history.
	closedUnresolvedHeldByGone := mkTicket(&gone, false, true)
	heldByDisabled := mkTicket(&disabled, false, false)
	heldByLive := mkTicket(&live, false, false)
	unassigned := mkTicket(nil, false, false)

	assignee := func(id uuid.UUID) *uuid.UUID {
		var a uuid.NullUUID
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT assignee_user_id FROM tickets WHERE id = $1`, id).Scan(&a))
		if !a.Valid {
			return nil
		}
		return &a.UUID
	}
	auditRows := func(id uuid.UUID) (n int, actor sql.NullString, before, after map[string]any) {
		rows, err := tx.QueryContext(ctx, `SELECT actor_id::text, action, before, after FROM audit_log
			WHERE entity_type = 'ticket' AND entity_id = $1 AND action = 'unassigned'`, id)
		require.NoError(t, err)
		defer rows.Close()
		for rows.Next() {
			var action string
			var b, a []byte
			require.NoError(t, rows.Scan(&actor, &action, &b, &a))
			require.NoError(t, json.Unmarshal(b, &before))
			require.NoError(t, json.Unmarshal(a, &after))
			n++
		}
		require.NoError(t, rows.Err())
		return n, actor, before, after
	}

	execMigrationFile(t, ctx, tx, "migrations/000053_unassign_deleted_assignees.up.sql", nil)

	require.Nil(t, assignee(heldByGone), "an open ticket held by a deleted user goes back to the queue")
	require.Nil(t, assignee(heldByGoneToo))
	n, actor, before, after := auditRows(heldByGone)
	require.Equal(t, 1, n, "one audit entry per ticket moved")
	require.False(t, actor.Valid, "no person is behind a migration")
	// The same shape ticket.Service.UnassignForUser writes.
	require.Equal(t, map[string]any{"assignee_user_id": gone.String()}, before)
	require.Equal(t, map[string]any{"assignee_user_id": nil}, after)
	n, _, before, _ = auditRows(heldByGoneToo)
	require.Equal(t, 1, n)
	require.Equal(t, map[string]any{"assignee_user_id": goneToo.String()}, before, "each entry names its own ticket's assignee")

	// Resolved and closed tickets are history, as in UnassignForUser.
	require.Equal(t, &gone, assignee(resolvedHeldByGone))
	require.Equal(t, &gone, assignee(closedHeldByGone))
	require.Equal(t, &gone, assignee(closedUnresolvedHeldByGone), "closed is closed, resolved or not")
	for _, id := range []uuid.UUID{resolvedHeldByGone, closedHeldByGone, closedUnresolvedHeldByGone} {
		n, _, _, _ = auditRows(id)
		require.Zero(t, n, "history is left alone and unaudited")
	}

	// A disabled user is still in the user list, so nothing is stranded.
	require.Equal(t, &disabled, assignee(heldByDisabled))
	require.Equal(t, &live, assignee(heldByLive))
	require.Nil(t, assignee(unassigned))
	for _, id := range []uuid.UUID{heldByDisabled, heldByLive, unassigned} {
		n, _, _, _ = auditRows(id)
		require.Zero(t, n)
	}

	// Run again: nothing left to move, so no second entry.
	execMigrationFile(t, ctx, tx, "migrations/000053_unassign_deleted_assignees.up.sql", nil)
	n, _, _, _ = auditRows(heldByGone)
	require.Equal(t, 1, n, "re-running adds nothing")
}
