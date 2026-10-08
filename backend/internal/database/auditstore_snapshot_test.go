package database_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// SpyTxBeginner records BeginTx calls and their options, delegating to a real DB.
type SpyTxBeginner struct {
	db          *sql.DB
	CallCount   int
	LastOptions *sql.TxOptions
}

func (s *SpyTxBeginner) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	s.CallCount++
	s.LastOptions = opts
	return s.db.BeginTx(ctx, opts)
}

// TestAuditStore_Search_SnapshotOn verifies that scoped searches run
// GetAuditTicketScope, SearchAuditLogScoped, and CountAuditLogScoped within
// a single repeatable-read transaction, preventing race conditions between
// membership changes and result visibility.
func TestAuditStore_Search_SnapshotOn(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	f := newFilteredFixture(t, q) // staff with groups and scope rules

	// Test with SnapshotOn: should make exactly one BeginTx call with RepeatableRead.
	spy := &SpyTxBeginner{db: db.SQL}
	aus := auditstore.New(q).SnapshotOn(spy)

	// Perform a scoped search.
	filter := audit.Filter{ScopedTo: &f.staff.ID}
	_, err := aus.Search(ctx, filter, 10, 0)
	require.NoError(t, err)

	require.Equal(t, 1, spy.CallCount, "expected exactly one BeginTx call for scoped search")
	require.NotNil(t, spy.LastOptions, "expected TxOptions to be set")
	require.Equal(t, sql.LevelRepeatableRead, spy.LastOptions.Isolation,
		"expected isolation level to be RepeatableRead")
	require.True(t, spy.LastOptions.ReadOnly, "expected ReadOnly to be true")

	// Test unscoped search: should make zero BeginTx calls.
	spy2 := &SpyTxBeginner{db: db.SQL}
	aus2 := auditstore.New(q).SnapshotOn(spy2)

	filter2 := audit.Filter{} // ScopedTo is nil
	_, err = aus2.Search(ctx, filter2, 10, 0)
	require.NoError(t, err)

	require.Equal(t, 0, spy2.CallCount, "expected zero BeginTx calls for unscoped search")
}

// TestAuditStore_Search_NoSnapshot verifies that searches without SnapshotOn
// do not attempt to begin transactions (they use the caller's Queries).
func TestAuditStore_Search_NoSnapshot(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	f := newFilteredFixture(t, q)

	// No SnapshotOn: Store has no snap field, should not error.
	aus := auditstore.New(q)
	filter := audit.Filter{ScopedTo: &f.staff.ID}
	_, err := aus.Search(ctx, filter, 10, 0)
	require.NoError(t, err)
}
