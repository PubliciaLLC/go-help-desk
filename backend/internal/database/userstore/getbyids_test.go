package userstore_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// GetByIDs is the batch behind the names in audit diffs (#422). It answers like
// GetByID does: an account that is soft-deleted, or never existed, is absent.
func TestGetByIDs(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	// Rolled back, so the users made here are not left for the setup tests,
	// which need an empty users table.
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	store := userstore.New(q)
	ctx := context.Background()

	a, b, gone := seedStaff(t, store), seedStaff(t, store), seedStaff(t, store)
	require.NoError(t, store.SoftDelete(ctx, gone))

	got, err := store.GetByIDs(ctx, []uuid.UUID{a, b, gone, uuid.New()})
	require.NoError(t, err)
	var ids []uuid.UUID
	for _, u := range got {
		ids = append(ids, u.ID)
	}
	require.ElementsMatch(t, []uuid.UUID{a, b}, ids)
}
