package database_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #418: foreign-key violations map to domain sentinels, detected by constraint
// name. A failed statement aborts its transaction, so every case gets its own.

// Creating a type or item under a parent that vanished between the service's
// existence check and the insert is a 23503; it must surface as ErrNotFound
// (404), not a raw driver error (500).
func TestCategoryStore_CreateUnderMissingParent_IsErrNotFound(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	t.Run("type under missing category", func(t *testing.T) {
		q, rollback := testutil.TxQueries(t, db)
		defer rollback()
		err := categorystore.New(q).CreateType(ctx, category.Type{ID: uuid.New(), CategoryID: uuid.New(), Name: "T", Active: true})
		require.ErrorIs(t, err, category.ErrNotFound)
	})
	t.Run("item under missing type", func(t *testing.T) {
		q, rollback := testutil.TxQueries(t, db)
		defer rollback()
		err := categorystore.New(q).CreateItem(ctx, category.Item{ID: uuid.New(), TypeID: uuid.New(), Name: "I", Active: true})
		require.ErrorIs(t, err, category.ErrNotFound)
	})
}

func TestGroupStore_ForeignKeyViolations(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		run  func(t *testing.T, gs *groupstore.Store, cs *categorystore.Store, g group.Group) error
		want error
	}{
		{"member of unknown group", func(t *testing.T, gs *groupstore.Store, _ *categorystore.Store, _ group.Group) error {
			return gs.AddMember(ctx, uuid.New(), uuid.New())
		}, group.ErrNotFound},
		{"unknown user", func(t *testing.T, gs *groupstore.Store, _ *categorystore.Store, g group.Group) error {
			return gs.AddMember(ctx, g.ID, uuid.New())
		}, group.ErrInvalidReference},
		{"scope of unknown group", func(t *testing.T, gs *groupstore.Store, _ *categorystore.Store, _ group.Group) error {
			return gs.AddScope(ctx, group.GroupScope{GroupID: uuid.New(), CategoryID: uuid.New()})
		}, group.ErrNotFound},
		{"unknown category", func(t *testing.T, gs *groupstore.Store, _ *categorystore.Store, g group.Group) error {
			return gs.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: uuid.New()})
		}, group.ErrInvalidReference},
		{"unknown type", func(t *testing.T, gs *groupstore.Store, cs *categorystore.Store, g group.Group) error {
			c := category.Category{ID: uuid.New(), Name: "C", Active: true}
			require.NoError(t, cs.CreateCategory(ctx, c))
			unknown := uuid.New()
			return gs.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: c.ID, TypeID: &unknown})
		}, group.ErrInvalidReference},
		{"type of another category", func(t *testing.T, gs *groupstore.Store, cs *categorystore.Store, g group.Group) error {
			a := category.Category{ID: uuid.New(), Name: "A", Active: true}
			b := category.Category{ID: uuid.New(), Name: "B", Active: true}
			require.NoError(t, cs.CreateCategory(ctx, a))
			require.NoError(t, cs.CreateCategory(ctx, b))
			tb := category.Type{ID: uuid.New(), CategoryID: b.ID, Name: "TB", Active: true}
			require.NoError(t, cs.CreateType(ctx, tb))
			return gs.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: a.ID, TypeID: &tb.ID})
		}, group.ErrInvalidReference},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, rollback := testutil.TxQueries(t, db)
			defer rollback()
			gs, cs := groupstore.New(q), categorystore.New(q)
			g := group.Group{ID: uuid.New(), Name: "G"}
			require.NoError(t, gs.Create(ctx, g))
			require.ErrorIs(t, tc.run(t, gs, cs, g), tc.want)
		})
	}
}
