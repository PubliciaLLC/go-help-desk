package database_test

import (
	"context"
	"os"
	"strings"
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

// ListResolvedTicketsBefore is only really tested against Postgres: the bug
// (#191) is entirely in the SQL predicate, not in Go, so a fake store proves
// nothing about it.
//
// This runs the real query against a row that satisfies resolved_at < cutoff
// but sits in a status other than Resolved — exactly the shape of a legacy
// row that, pre-fix, was listed on every sweep, locked, skipped, and listed
// again forever.
func TestTicketStore_ListResolvedBefore_ExcludesWrongStatus(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "resolved-before-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "RB " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	resolvedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameResolved)
	require.NoError(t, err)
	closedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameClosed)
	require.NoError(t, err)

	old := time.Now().UTC().Add(-10 * 24 * time.Hour).Truncate(time.Millisecond)
	mk := func(subject string, statusID uuid.UUID, resolvedAt *time.Time) ticket.Ticket {
		tk := ticket.Ticket{
			ID:             uuid.New(),
			TrackingNumber: ticket.TrackingNumber("RB-" + uuid.NewString()[:8]),
			Subject:        subject,
			Description:    subject,
			CategoryID:     cat.ID,
			Priority:       ticket.PriorityMedium,
			StatusID:       statusID,
			ReporterUserID: &reporter.ID,
			ResolvedAt:     resolvedAt,
			CreatedAt:      old.Add(-24 * time.Hour),
			UpdatedAt:      old,
		}
		require.NoError(t, ts.Create(ctx, tk))
		// Create never sets resolved_at/closed_at (a new ticket cannot start
		// resolved) — this test needs a row that already carries one, which is
		// what a status change writes, so it goes through Update the same way.
		require.NoError(t, ts.Update(ctx, tk))
		return tk
	}

	// Genuinely eligible: in Resolved, resolved_at in the past.
	eligible := mk("RB eligible", resolvedSt.ID, &old)
	// Legacy row: resolved_at set, but moved back to New without clearing it.
	legacyOpen := mk("RB legacy open", newSt.ID, &old)
	// Legacy row: resolved_at stale, ticket already Closed.
	legacyClosed := mk("RB legacy closed", closedSt.ID, &old)

	got, err := ts.ListResolvedBefore(ctx, time.Now(), resolvedSt.ID, 500)
	require.NoError(t, err)

	ids := map[uuid.UUID]bool{}
	for _, tk := range got {
		ids[tk.ID] = true
	}
	require.True(t, ids[eligible.ID], "a ticket genuinely in Resolved past the cutoff must be listed")
	require.False(t, ids[legacyOpen.ID], "a row not in Resolved must never be listed, even with a stale resolved_at")
	require.False(t, ids[legacyClosed.ID], "a Closed ticket with a stale resolved_at must never be listed")
}

// TestMigration_RepairsResolvedStatusInvariant runs migration 000028's exact
// SQL (read from disk, not reimplemented) against rows seeded directly to
// violate the invariant it repairs — a stale resolved_at on a ticket no
// longer in Resolved, and a Closed ticket with no closed_at — and checks it
// fixes them, and only them.
func TestMigration_RepairsResolvedStatusInvariant(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	sql, err := os.ReadFile("migrations/000028_repair_resolved_status_invariant.up.sql")
	require.NoError(t, err)

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	ctx := context.Background()
	q := dbgen.New(tx)

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "migration-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "Mig " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	resolvedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameResolved)
	require.NoError(t, err)
	closedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameClosed)
	require.NoError(t, err)

	stale := time.Now().UTC().Add(-30 * 24 * time.Hour).Truncate(time.Millisecond)
	mk := func(subject string, statusID uuid.UUID, resolvedAt, closedAt *time.Time) ticket.Ticket {
		tk := ticket.Ticket{
			ID:             uuid.New(),
			TrackingNumber: ticket.TrackingNumber("MIG-" + uuid.NewString()[:8]),
			Subject:        subject,
			Description:    subject,
			CategoryID:     cat.ID,
			Priority:       ticket.PriorityMedium,
			StatusID:       statusID,
			ReporterUserID: &reporter.ID,
			ResolvedAt:     resolvedAt,
			ClosedAt:       closedAt,
			CreatedAt:      stale.Add(-24 * time.Hour),
			UpdatedAt:      stale,
		}
		require.NoError(t, ts.Create(ctx, tk))
		// Create never sets resolved_at/closed_at; write them the way a status
		// change would, directly, to seed the pre-#191 invariant violation.
		require.NoError(t, ts.Update(ctx, tk))
		return tk
	}

	// Bad row #1: moved off Resolved, resolved_at never cleared.
	badOpen := mk("Mig bad open", newSt.ID, &stale, nil)
	// Bad row #2: sitting in Closed with no closed_at.
	badClosed := mk("Mig bad closed", closedSt.ID, nil, nil)
	// Bad row #3: sitting in Resolved with a stale closed_at left over from
	// resolving a previously-Closed ticket before that bug was fixed.
	badResolvedStaleClosed := mk("Mig bad resolved stale closed", resolvedSt.ID, &stale, &stale)
	// Control: correctly Resolved, must keep its timestamp.
	goodResolved := mk("Mig good resolved", resolvedSt.ID, &stale, nil)
	// Control: an ordinary open ticket with nothing set, must stay untouched.
	goodOpen := mk("Mig good open", newSt.ID, nil, nil)
	// Control: the ordinary, correct Closed shape — both resolved_at and
	// closed_at set (applyStatusTimestamps's closedID case never clears
	// ResolvedAt). Must survive untouched; see #208.
	goodClosed := mk("Mig good closed", closedSt.ID, &stale, &stale)

	// Run the migration's own SQL, verbatim (comments stripped first, since a
	// naive split on ";" would otherwise break mid-statement on the semicolons
	// inside the file's prose comments).
	var stripped strings.Builder
	for _, line := range strings.Split(string(sql), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		stripped.WriteString(line)
		stripped.WriteByte('\n')
	}
	for _, stmt := range strings.Split(stripped.String(), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		_, err := tx.ExecContext(ctx, stmt)
		require.NoError(t, err, "executing migration statement: %s", stmt)
	}

	get := func(id uuid.UUID) ticket.Ticket {
		tk, err := ts.GetByID(ctx, id)
		require.NoError(t, err)
		return tk
	}

	repairedOpen := get(badOpen.ID)
	require.Nil(t, repairedOpen.ResolvedAt, "a ticket not in Resolved must have resolved_at cleared")

	repairedClosed := get(badClosed.ID)
	require.NotNil(t, repairedClosed.ClosedAt, "a Closed ticket must be stamped with closed_at")

	stillResolved := get(goodResolved.ID)
	require.NotNil(t, stillResolved.ResolvedAt, "a correctly-Resolved ticket must keep its timestamp")

	stillOpen := get(goodOpen.ID)
	require.Nil(t, stillOpen.ResolvedAt)
	require.Nil(t, stillOpen.ClosedAt)

	repairedResolved := get(badResolvedStaleClosed.ID)
	require.NotNil(t, repairedResolved.ResolvedAt, "a Resolved ticket must keep its resolved_at")
	require.Nil(t, repairedResolved.ClosedAt, "a Resolved ticket must have a stale closed_at cleared")

	stillClosed := get(goodClosed.ID)
	require.NotNil(t, stillClosed.ResolvedAt, "a Closed ticket must NOT have resolved_at cleared (#208)")
	require.NotNil(t, stillClosed.ClosedAt, "a Closed ticket must keep its closed_at")
}
