package tagstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/tagstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/tag"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

func newStore(t *testing.T) (*tagstore.Store, func()) {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	q, rollback := testutil.TxQueries(t, db)
	return tagstore.New(q), func() { rollback(); closeDB() }
}

// #528: a delete or restore that touches no row says so, instead of looking
// like it worked.
func TestSoftDelete_UnknownIDIsNotFound(t *testing.T) {
	s, done := newStore(t)
	defer done()

	require.ErrorIs(t, s.SoftDelete(context.Background(), uuid.New()), tag.ErrNotFound)
}

func TestRestore_UnknownIDIsNotFound(t *testing.T) {
	s, done := newStore(t)
	defer done()

	require.ErrorIs(t, s.Restore(context.Background(), uuid.New()), tag.ErrNotFound)
}

// Deleting a tag that is already deleted finds it, so it is not an error, and
// it must not move the time it was first deleted.
func TestSoftDelete_AlreadyDeletedKeepsItsOriginalTime(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	s := tagstore.New(q)
	ctx := context.Background()

	created, err := s.Create(ctx, "once-twice")
	require.NoError(t, err)
	then := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	_, err = tx.ExecContext(ctx, `UPDATE tags SET deleted_at = $2 WHERE id = $1`, created.ID, then)
	require.NoError(t, err)

	require.NoError(t, s.SoftDelete(ctx, created.ID))

	got, err := s.GetByName(ctx, "once-twice")
	require.NoError(t, err)
	require.NotNil(t, got.DeletedAt)
	require.True(t, got.DeletedAt.Equal(then), "deleted_at moved from %s to %s", then, got.DeletedAt)
}

func TestSoftDeleteThenRestore(t *testing.T) {
	s, done := newStore(t)
	defer done()
	ctx := context.Background()

	created, err := s.Create(ctx, "round-trip")
	require.NoError(t, err)
	require.NoError(t, s.SoftDelete(ctx, created.ID))
	got, err := s.GetByName(ctx, "round-trip")
	require.NoError(t, err)
	require.NotNil(t, got.DeletedAt)

	require.NoError(t, s.Restore(ctx, created.ID))
	got, err = s.GetByName(ctx, "round-trip")
	require.NoError(t, err)
	require.Nil(t, got.DeletedAt)
}
