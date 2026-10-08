package database_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// SpyTxBeginner records BeginTx calls and their options, returning a specific
// transaction. Used to verify SnapshotOn creates transactions with the correct
// isolation level and read-only flag.
type SpyTxBeginner struct {
	tx          *sql.Tx
	CallCount   int
	LastOptions *sql.TxOptions
}

func (s *SpyTxBeginner) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	s.CallCount++
	s.LastOptions = opts
	// Return the same tx that was provided.
	return s.tx, nil
}

// TestAuditStore_Search_SnapshotOn_Scoped verifies that scoped searches run
// GetAuditTicketScope, SearchAuditLogScoped, and CountAuditLogScoped within
// a single repeatable-read transaction, preventing race conditions between
// membership changes and result visibility. The snapshot store is given a
// closed DB for its Queries, so any read not through the snapshot transaction
// will fail, catching mutations that skip the tx binding.
func TestAuditStore_Search_SnapshotOn_Scoped(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()

	ctx := context.Background()

	// Build fixture: staff user and visible tickets.
	us := userstore.New(q)
	ts := ticketstore.New(q)
	cs := categorystore.New(q)

	cat := category.Category{ID: uuid.New(), Name: "SnapCat", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	staff := user.User{
		ID:          uuid.New(),
		Email:       "snap-staff@example.com",
		DisplayName: "Snap Staff",
		Role:        user.RoleStaff,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, staff))

	reporter := user.User{
		ID:          uuid.New(),
		Email:       "snap-reporter@example.com",
		DisplayName: "Snap Reporter",
		Role:        user.RoleUser,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))

	other := user.User{
		ID:          uuid.New(),
		Email:       "snap-other@example.com",
		DisplayName: "Snap Other",
		Role:        user.RoleUser,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, other))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Millisecond)
	seq := int64(7000)

	mkTicket := func(subject string, reporterID *uuid.UUID, assigneeID *uuid.UUID) ticket.Ticket {
		seq++
		tk := ticket.Ticket{
			ID:             uuid.New(),
			TrackingNumber: ticket.GenerateTrackingNumber(ticket.DefaultTrackingPrefix, 2026, seq),
			Subject:        subject,
			Description:    subject + " description",
			CategoryID:     cat.ID,
			Priority:       ticket.PriorityLow,
			StatusID:       newSt.ID,
			ReporterUserID: reporterID,
			AssigneeUserID: assigneeID,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		require.NoError(t, ts.Create(ctx, tk))
		return tk
	}

	// Two tickets staff can see.
	ticketByStaff := mkTicket("Snap ticket by staff", &staff.ID, nil)
	ticketAssignedToStaff := mkTicket("Snap ticket assigned to staff", &reporter.ID, &staff.ID)

	// Two tickets staff cannot see.
	ticketByReporter := mkTicket("Snap ticket by reporter", &reporter.ID, nil)
	ticketByOther := mkTicket("Snap ticket by other", &other.ID, nil)

	// Plant audit entries on visible tickets.
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	nEntries := 0

	plantEntry := func(entityID uuid.UUID) uuid.UUID {
		nEntries++
		eid := uuid.New()
		require.NoError(t, q.CreateAuditEntry(ctx, dbgen.CreateAuditEntryParams{
			ID:         eid,
			EntityType: "ticket",
			EntityID:   entityID,
			Action:     "test_snapshot",
			CreatedAt:  at.Add(time.Duration(nEntries) * time.Second),
		}))
		return eid
	}

	// Plant 2 entries on each visible ticket, 1 on each invisible.
	for i := 0; i < 2; i++ {
		plantEntry(ticketByStaff.ID)
	}
	for i := 0; i < 2; i++ {
		plantEntry(ticketAssignedToStaff.ID)
	}
	plantEntry(ticketByReporter.ID)
	plantEntry(ticketByOther.ID)

	// Compute expected result FIRST using plain store, before running snapshot.
	plainStore := auditstore.New(q)
	filter := audit.Filter{
		Action:   "test_snapshot",
		ScopedTo: &staff.ID,
	}
	expectedPage, err := plainStore.Search(ctx, filter, 10, 0)
	require.NoError(t, err)
	require.NotEmpty(t, expectedPage.Entries, "expected visible audit entries in fixture")
	require.Equal(t, 4, expectedPage.Total, "expected exactly 4 visible audit entries (2 tickets × 2 entries each)")
	require.False(t, expectedPage.TotalCapped)

	expectedIDs := make(map[uuid.UUID]bool)
	for _, e := range expectedPage.Entries {
		expectedIDs[e.ID] = true
	}
	require.Len(t, expectedIDs, 4, "expected 4 unique entries")

	// Create a closed DB so reads not through the snapshot tx will fail.
	dsn := os.Getenv("TEST_DATABASE_URL")
	closedDB, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	require.NoError(t, closedDB.Close())

	// Snapshot store uses the closed DB and spy.
	spy := &SpyTxBeginner{tx: tx}
	snapshotStore := auditstore.New(dbgen.New(closedDB)).SnapshotOn(spy)

	// Perform a scoped search.
	snapshotPage, err := snapshotStore.Search(ctx, filter, 10, 0)
	require.NoError(t, err)

	// Verify exactly one BeginTx call with correct isolation and read-only flag.
	require.Equal(t, 1, spy.CallCount, "expected exactly one BeginTx call for scoped search")
	require.NotNil(t, spy.LastOptions, "expected TxOptions to be set")
	require.Equal(t, sql.LevelRepeatableRead, spy.LastOptions.Isolation,
		"expected isolation level to be RepeatableRead")
	require.True(t, spy.LastOptions.ReadOnly, "expected ReadOnly to be true")

	// Verify snapshot store returns the same results as the plain store.
	require.Equal(t, expectedPage.Total, snapshotPage.Total,
		"snapshot store Total must equal plain store Total")
	require.False(t, snapshotPage.TotalCapped, "expected TotalCapped to be false")
	require.Equal(t, len(expectedPage.Entries), len(snapshotPage.Entries),
		"snapshot store Entries count must equal plain store count")

	snapshotIDs := make(map[uuid.UUID]bool)
	for _, e := range snapshotPage.Entries {
		snapshotIDs[e.ID] = true
	}
	require.Equal(t, expectedIDs, snapshotIDs,
		"snapshot store Entries must be the same as plain store (same IDs, same count)")
}

// TestAuditStore_Search_SnapshotOn_Unscoped verifies that unscoped searches
// do not begin transactions. The unscoped path runs with the caller's Queries
// directly, not through a snapshot tx, so SnapshotOn should have no effect.
func TestAuditStore_Search_SnapshotOn_Unscoped(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()

	ctx := context.Background()

	// Build a minimal fixture: create at least one audit entry.
	us := userstore.New(q)
	ts := ticketstore.New(q)
	cs := categorystore.New(q)

	cat := category.Category{ID: uuid.New(), Name: "UnscopedCat", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	staff := user.User{
		ID:          uuid.New(),
		Email:       "unscoped-staff@example.com",
		DisplayName: "Unscoped Staff",
		Role:        user.RoleStaff,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, staff))

	reporter := user.User{
		ID:          uuid.New(),
		Email:       "unscoped-reporter@example.com",
		DisplayName: "Unscoped Reporter",
		Role:        user.RoleUser,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Millisecond)
	tk := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: ticket.GenerateTrackingNumber(ticket.DefaultTrackingPrefix, 2026, 8000),
		Subject:        "Unscoped test ticket",
		Description:    "Unscoped test ticket description",
		CategoryID:     cat.ID,
		Priority:       ticket.PriorityLow,
		StatusID:       newSt.ID,
		ReporterUserID: &reporter.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	require.NoError(t, ts.Create(ctx, tk))

	// Plant an audit entry.
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	require.NoError(t, q.CreateAuditEntry(ctx, dbgen.CreateAuditEntryParams{
		ID:         uuid.New(),
		EntityType: "ticket",
		EntityID:   tk.ID,
		Action:     "test_unscoped",
		CreatedAt:  at,
	}))

	// Compute expected result with plain store BEFORE creating snapshot store.
	plainStore := auditstore.New(q)
	expectedPage, err := plainStore.Search(ctx, audit.Filter{Action: "test_unscoped"}, 10, 0)
	require.NoError(t, err)
	require.NotEmpty(t, expectedPage.Entries, "expected audit entries for unscoped search")

	// For unscoped searches, the store uses the caller's Queries directly (q),
	// regardless of SnapshotOn. Create a spy and snapshot store using q.
	spy := &SpyTxBeginner{tx: tx}
	snapshotStore := auditstore.New(q).SnapshotOn(spy)

	// Perform an UNSCOPED search (ScopedTo is nil).
	filter := audit.Filter{Action: "test_unscoped"} // ScopedTo is nil
	snapshotPage, err := snapshotStore.Search(ctx, filter, 10, 0)
	require.NoError(t, err)

	// For unscoped searches, SnapshotOn should NOT cause a BeginTx call.
	// The unscoped path uses searchUnscoped which calls s.q directly, not the snapshot tx.
	require.Equal(t, 0, spy.CallCount, "expected zero BeginTx calls for unscoped search")

	// Verify results match plain store.
	require.Equal(t, expectedPage.Total, snapshotPage.Total,
		"unscoped snapshot store Total must equal plain store Total")
	require.Equal(t, len(expectedPage.Entries), len(snapshotPage.Entries),
		"unscoped snapshot store Entries count must equal plain store count")

	expectedIDSet := make(map[uuid.UUID]bool)
	for _, e := range expectedPage.Entries {
		expectedIDSet[e.ID] = true
	}
	snapshotIDSet := make(map[uuid.UUID]bool)
	for _, e := range snapshotPage.Entries {
		snapshotIDSet[e.ID] = true
	}
	require.Equal(t, expectedIDSet, snapshotIDSet,
		"unscoped snapshot store Entries must match plain store")
}
