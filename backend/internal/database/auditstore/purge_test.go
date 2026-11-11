package auditstore_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// hookedTx counts the statements a store runs and lets a test act after each
// write, which is how the purge is observed batch by batch.
type hookedTx struct {
	dbgen.DBTX
	execs   int
	afterEx func()
}

func (h *hookedTx) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	res, err := h.DBTX.ExecContext(ctx, q, args...)
	h.execs++
	if h.afterEx != nil {
		h.afterEx()
	}
	return res, err
}

// seedAncient writes n entries dated 1970, so a cutoff of February 1970 matches
// exactly these and nothing else a long-lived test database might hold.
func seedAncient(t *testing.T, q *dbgen.Queries, n int) time.Time {
	t.Helper()
	for i := 0; i < n; i++ {
		require.NoError(t, q.CreateAuditEntry(context.Background(), dbgen.CreateAuditEntryParams{
			ID: uuid.New(), EntityType: "purge_test", EntityID: uuid.New(), Action: "created",
			CreatedAt: time.Date(1970, 1, 1, 0, 0, i, 0, time.UTC),
		}))
	}
	return time.Date(1970, 2, 1, 0, 0, 0, 0, time.UTC)
}

// #629(c): the retention purge deletes in batches, so the first purge of a big
// log is not one statement holding a lock on every expired row. The count it
// reports is the sum over the batches.
func TestDeleteOlderThan_DeletesInBatchesAndReportsTheTotal(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()

	cutoff := seedAncient(t, q, 7)
	h := &hookedTx{DBTX: tx}
	s := auditstore.NewWithPurgeBatchForTest(dbgen.New(h), 3)

	n, err := s.DeleteOlderThan(context.Background(), cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(7), n)
	require.Equal(t, 3, h.execs, "7 rows in batches of 3 is 3, 3 and 1")

	var left int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM audit_log WHERE entity_type = 'purge_test'`).Scan(&left))
	require.Zero(t, left)
}

// A whole number of batches ends on an empty one, not on a guess.
func TestDeleteOlderThan_AWholeNumberOfBatches(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()

	cutoff := seedAncient(t, q, 6)
	h := &hookedTx{DBTX: tx}
	s := auditstore.NewWithPurgeBatchForTest(dbgen.New(h), 3)

	n, err := s.DeleteOlderThan(context.Background(), cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(6), n)
}

// Between batches the context is checked, so a shutdown does not wait for the
// rest of a long purge, and what was already deleted is still reported.
func TestDeleteOlderThan_StopsBetweenBatchesWhenCancelled(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()

	cutoff := seedAncient(t, q, 7)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &hookedTx{DBTX: tx, afterEx: cancel}
	s := auditstore.NewWithPurgeBatchForTest(dbgen.New(h), 3)

	n, err := s.DeleteOlderThan(ctx, cutoff)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, int64(3), n, "the first batch committed before the cancel")
	require.Equal(t, 1, h.execs, "no second batch after the cancel")

	var left int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM audit_log WHERE entity_type = 'purge_test'`).Scan(&left))
	require.Equal(t, 4, left)
}
