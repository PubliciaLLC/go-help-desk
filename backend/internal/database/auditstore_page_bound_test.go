package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// sqlc emits int32 for LIMIT and OFFSET, and a Go int past MaxInt32 used to wrap:
// 2147483648 went negative (Postgres answers "OFFSET must not be negative", a
// 500) and 4294967296 wrapped to zero and served the first page. A page past the
// end is an empty page, whatever the number.
func TestAuditStore_PageBoundsPastInt32AreAnEmptyPage(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()

	ctx := context.Background()
	aus := auditstore.New(q)
	const action = "page_bound_probe"
	entity := uuid.New()
	plantMany(t, tx, "ticket", entity, action, 3, time.Now().Add(-time.Hour).UTC().Truncate(time.Second))

	got, err := aus.ListByEntityActions(ctx, "ticket", entity, audit.ActionFilter{}, 10, 0)
	require.NoError(t, err)
	require.Len(t, got, 3, "the rows are there on the first page")

	for _, off := range []int{2147483648, 4294967296} {
		got, err := aus.ListByEntityActions(ctx, "ticket", entity, audit.ActionFilter{}, 10, off)
		require.NoError(t, err, "offset %d", off)
		require.Empty(t, got, "offset %d is past the end, not page one", off)
	}
}
