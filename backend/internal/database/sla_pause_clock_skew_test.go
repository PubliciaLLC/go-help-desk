package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// TestUpdateStatus_LeavingPendingUnderClockSkewDoesNotFailTheCheckConstraint
// pins #223 against the real database: migration 000025's
// CHECK (sla_paused_seconds >= 0) rejects a negative delta outright, and
// PendingSince may have been written by a different app replica than the one
// computing now — with clock skew between them, leaving Pending can compute a
// negative interval. Simulated here with a PendingSince in the future
// (negative skew). Before the clamp in applyStatusTimestamps, this failed the
// whole status-change transaction with a raw constraint-violation 500; after
// it, the transition simply contributes nothing negative and commits.
func TestUpdateStatus_LeavingPendingUnderClockSkewDoesNotFailTheCheckConstraint(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()

	q := db.Queries
	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	auStore := auditstore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "skew-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "Skew " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	pendingSt, err := ts.GetStatusByName(ctx, ticket.StatusNamePending)
	require.NoError(t, err)
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Millisecond)
	pendingSince := now.Add(30 * time.Second) // in the future: negative skew when left "now"
	tk := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("SKEW-" + uuid.NewString()[:8]),
		Subject: "Clock skew", Description: "Clock skew", CategoryID: cat.ID,
		Priority: ticket.PriorityMedium, StatusID: pendingSt.ID, ReporterUserID: &reporter.ID,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		PendingSince: &pendingSince,
	}
	require.NoError(t, ts.Create(ctx, tk))
	require.NoError(t, ts.Update(ctx, tk)) // Create doesn't persist PendingSince; write it directly

	t.Cleanup(func() {
		_, _ = db.SQL.Exec(`DELETE FROM tickets WHERE id = $1`, tk.ID)
		_, _ = db.SQL.Exec(`DELETE FROM users WHERE id = $1`, reporter.ID)
		_, _ = db.SQL.Exec(`DELETE FROM categories WHERE id = $1`, cat.ID)
	})

	svc := ticket.NewService(ts, ts, notification.Noop{}, auStore, txrunner.New(db.SQL), nil)
	require.NoError(t, svc.LoadSystemStatuses(ctx))

	staff := ticket.Actor{UserID: &reporter.ID, Role: user.RoleStaff}
	_, err = svc.UpdateStatus(ctx, tk.ID, newSt.ID, staff)
	require.NoError(t, err, "leaving Pending under clock skew must not fail the sla_paused_seconds >= 0 CHECK")

	stored, err := ts.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	require.Nil(t, stored.PendingSince, "the interval must still close")
	require.Zero(t, stored.SLAPausedSeconds, "a negative delta must contribute nothing")
}
