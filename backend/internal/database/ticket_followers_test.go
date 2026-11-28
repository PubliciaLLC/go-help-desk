package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Ticket followers (#441): the table, its key and its cascades, and the two
// reads staff mail and the followers list depend on.
func TestTicketFollowers_Store(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	ctx := context.Background()
	us, cs, ts := userstore.New(q), categorystore.New(q), ticketstore.New(q)

	now := time.Now().UTC().Truncate(time.Millisecond)
	mk := func(name string, role user.Role) user.User {
		u := user.User{ID: uuid.New(), Email: name + "@followers.test", DisplayName: name, Role: role, CreatedAt: now, UpdatedAt: now}
		require.NoError(t, us.Create(ctx, u))
		return u
	}
	staff, admin, disabled, deleted, requester, bot := mk("staff", user.RoleStaff), mk("admin", user.RoleAdmin),
		mk("disabled", user.RoleStaff), mk("deleted", user.RoleStaff), mk("requester", user.RoleUser), mk("bot", user.RoleStaff)
	_, err := tx.ExecContext(ctx, `UPDATE users SET disabled = TRUE WHERE id = $1`, disabled.ID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, deleted.ID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE users SET service_account = TRUE WHERE id = $1`, bot.ID)
	require.NoError(t, err)

	cat := category.Category{ID: uuid.New(), Name: "Followers", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	tk := ticket.Ticket{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		ID: uuid.New(), TrackingNumber: ticket.GenerateTrackingNumber(ticket.DefaultTrackingPrefix, 2026, 990001),
		Subject: "s", Description: "d", CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: newSt.ID, ReporterUserID: &requester.ID, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, ts.Create(ctx, tk))

	// The key: a second add changes nothing and says so.
	for i, u := range []user.User{staff, admin, disabled, deleted, requester, bot} {
		changed, err := ts.AddFollower(ctx, tk.ID, u.ID)
		require.NoError(t, err)
		require.True(t, changed, "first add of %d", i)
	}
	changed, err := ts.AddFollower(ctx, tk.ID, staff.ID)
	require.NoError(t, err)
	require.False(t, changed, "already following")

	// The list staff see leaves out only the deleted account.
	list, err := ts.ListFollowers(ctx, tk.ID)
	require.NoError(t, err)
	var names []string
	for _, f := range list {
		names = append(names, f.DisplayName)
		require.False(t, f.CreatedAt.IsZero())
	}
	require.ElementsMatch(t, []string{"staff", "admin", "disabled", "requester", "bot"}, names)

	// Who can be mailed: active staff and administrators, never a service
	// account, a disabled or deleted account, or somebody who is now a requester.
	ids, err := ts.ListMailableFollowerIDs(ctx, tk.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{staff.ID, admin.ID}, ids)

	// Another ticket's followers are its own.
	other, err := ts.ListFollowers(ctx, uuid.New())
	require.NoError(t, err)
	require.Empty(t, other)

	changed, err = ts.RemoveFollower(ctx, tk.ID, staff.ID)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = ts.RemoveFollower(ctx, tk.ID, staff.ID)
	require.NoError(t, err)
	require.False(t, changed, "not following")

	// A hard delete takes the rows with it, from either side.
	_, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, admin.ID)
	require.NoError(t, err)
	var n int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM ticket_followers WHERE user_id = $1`, admin.ID).Scan(&n))
	require.Zero(t, n)
	_, err = tx.ExecContext(ctx, `DELETE FROM tickets WHERE id = $1`, tk.ID)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM ticket_followers WHERE ticket_id = $1`, tk.ID).Scan(&n))
	require.Zero(t, n)
}
