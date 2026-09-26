package ticketstore_test

import (
	"context"
	"errors"
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

// TestDeleteStatus_TicketReferencingIt_ReturnsErrStatusInUse exercises
// isStatusInUseViolation's constraint-name matching against a REAL Postgres
// 23503, closing the gap TestIsStatusInUseViolation's own table (in
// status_violations_test.go) cannot: that table only proves the three
// hand-typed constraint-name strings match copies of themselves, so a typo
// present in both places would still pass every case. Here a ticket is
// created holding a custom status, and ticketstore.Store.DeleteStatus is
// called directly — bypassing ticket.Service.RemoveStatus's CountByStatus and
// CountStatusHistoryByStatus checks entirely, which is what normally keeps an
// ordinary admin request from ever reaching the database with a status still
// in use — to force the actual tickets_status_id_fkey violation and confirm
// it comes back wrapped in ticket.ErrStatusInUse rather than as a raw,
// unrecognized *pgconn.PgError. See #281.
//
// This lives in the external ticketstore_test package, alongside the
// unexported-function table in status_violations_test.go, rather than in
// that file: internal/testutil imports ticketstore itself (to build its
// JoiningTxRunner), so importing testutil from `package ticketstore` would be
// an import cycle.
func TestDeleteStatus_TicketReferencingIt_ReturnsErrStatusInUse(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)

	reporter := user.User{
		ID:          uuid.New(),
		Email:       "status-violation-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter",
		Role:        user.RoleUser,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))

	cat := category.Category{ID: uuid.New(), Name: "Status violation " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	custom := ticket.Status{
		ID:        uuid.New(),
		Name:      "Awaiting Parts " + uuid.NewString()[:8],
		Kind:      ticket.StatusKindCustom,
		SortOrder: 50,
		Color:     "#123456",
		Active:    true,
	}
	require.NoError(t, ts.CreateStatus(ctx, custom))

	tk := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: ticket.TrackingNumber("SV-" + uuid.NewString()[:8]),
		Subject:        "status violation test",
		Description:    "status violation test",
		CategoryID:     cat.ID,
		Priority:       ticket.PriorityMedium,
		StatusID:       custom.ID,
		ReporterUserID: &reporter.ID,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	require.NoError(t, ts.Create(ctx, tk))

	// Bypasses Service.RemoveStatus's counts entirely: the ticket above still
	// holds this status, so this hits the real tickets_status_id_fkey
	// violation rather than the count-based refusal an admin request would
	// see first.
	err := ts.DeleteStatus(ctx, custom.ID)
	require.Error(t, err)
	require.True(t, errors.Is(err, ticket.ErrStatusInUse), "got %v, want an error wrapping ticket.ErrStatusInUse", err)
}
