package ticketstore_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// An unknown group is reported as not found by both halves of a group delete,
// so the handler answers 404 and the transaction rolls back (#419).
func TestGroupDelete_UnknownGroupIsNotFound(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	store := ticketstore.New(dbgen.New(db.SQL))
	ctx := context.Background()

	_, err := store.UnassignForGroup(ctx, uuid.New())
	require.ErrorIs(t, err, group.ErrNotFound)
	require.ErrorIs(t, store.DeleteGroup(ctx, uuid.New()), group.ErrNotFound)
}
