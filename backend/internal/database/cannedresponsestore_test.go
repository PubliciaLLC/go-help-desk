package database_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database/cannedresponsestore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/cannedresponse"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
	"github.com/stretchr/testify/require"
)

func ptr(id uuid.UUID) *uuid.UUID { return &id }

func TestCannedResponseStore_CRUD(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := cannedresponsestore.New(q)
	ctx := context.Background()

	cr := cannedresponse.CannedResponse{
		ID:   uuid.New(),
		Name: "Acknowledgement",
		Body: "We received your request.",
	}
	require.NoError(t, s.Create(ctx, cr))

	got, err := s.Get(ctx, cr.ID)
	require.NoError(t, err)
	require.Equal(t, "Acknowledgement", got.Name)
	require.Nil(t, got.CategoryID)
	require.Nil(t, got.TypeID)

	// Update: rename and scope to a category.
	cs := categorystore.New(q)
	cat := category.Category{ID: uuid.New(), Name: "Hardware", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	got.Name = "Ack v2"
	got.CategoryID = ptr(cat.ID)
	require.NoError(t, s.Update(ctx, got))

	got2, err := s.Get(ctx, cr.ID)
	require.NoError(t, err)
	require.Equal(t, "Ack v2", got2.Name)
	require.NotNil(t, got2.CategoryID)
	require.Equal(t, cat.ID, *got2.CategoryID)

	require.NoError(t, s.Delete(ctx, cr.ID))
	_, err = s.Get(ctx, cr.ID)
	require.Error(t, err)
}

func TestCannedResponseStore_List_Sorted(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := cannedresponsestore.New(q)
	ctx := context.Background()

	require.NoError(t, s.Create(ctx, cannedresponse.CannedResponse{ID: uuid.New(), Name: "Bravo", Body: "b", SortOrder: 1}))
	require.NoError(t, s.Create(ctx, cannedresponse.CannedResponse{ID: uuid.New(), Name: "Alpha", Body: "a", SortOrder: 1}))
	require.NoError(t, s.Create(ctx, cannedresponse.CannedResponse{ID: uuid.New(), Name: "First", Body: "f", SortOrder: 0}))

	got, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, "First", got[0].Name) // sort_order 0
	require.Equal(t, "Alpha", got[1].Name) // sort_order 1, name A < B
	require.Equal(t, "Bravo", got[2].Name)
}

// The CHECK constraint must reject a type-scoped response with no category,
// independent of the domain-layer validation.
func TestCannedResponseStore_ScopeCheckConstraint(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	cs := categorystore.New(q)
	ctx := context.Background()
	cat := category.Category{ID: uuid.New(), Name: "Software", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	tp := category.Type{ID: uuid.New(), CategoryID: cat.ID, Name: "OS", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateType(ctx, tp))

	s := cannedresponsestore.New(q)
	err := s.Create(ctx, cannedresponse.CannedResponse{
		ID:     uuid.New(),
		Name:   "Bad",
		Body:   "x",
		TypeID: ptr(tp.ID), // CategoryID intentionally nil
	})
	require.Error(t, err)
}

func TestCannedResponseStore_CascadeOnCategoryDelete(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	cs := categorystore.New(q)
	s := cannedresponsestore.New(q)
	ctx := context.Background()

	cat := category.Category{ID: uuid.New(), Name: "Network", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	tp := category.Type{ID: uuid.New(), CategoryID: cat.ID, Name: "VPN", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateType(ctx, tp))

	global := cannedresponse.CannedResponse{ID: uuid.New(), Name: "Global", Body: "g"}
	catScoped := cannedresponse.CannedResponse{ID: uuid.New(), Name: "Cat", Body: "c", CategoryID: ptr(cat.ID)}
	typeScoped := cannedresponse.CannedResponse{ID: uuid.New(), Name: "Type", Body: "t", CategoryID: ptr(cat.ID), TypeID: ptr(tp.ID)}
	require.NoError(t, s.Create(ctx, global))
	require.NoError(t, s.Create(ctx, catScoped))
	require.NoError(t, s.Create(ctx, typeScoped))

	// Deleting the category cascades to both scoped responses; the global one stays.
	require.NoError(t, cs.DeleteCategory(ctx, cat.ID))

	got, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, global.ID, got[0].ID)
}

func TestCannedResponseStore_CascadeOnTypeDelete(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	cs := categorystore.New(q)
	s := cannedresponsestore.New(q)
	ctx := context.Background()

	cat := category.Category{ID: uuid.New(), Name: "Email", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	tp := category.Type{ID: uuid.New(), CategoryID: cat.ID, Name: "Outlook", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateType(ctx, tp))

	catScoped := cannedresponse.CannedResponse{ID: uuid.New(), Name: "Cat", Body: "c", CategoryID: ptr(cat.ID)}
	typeScoped := cannedresponse.CannedResponse{ID: uuid.New(), Name: "Type", Body: "t", CategoryID: ptr(cat.ID), TypeID: ptr(tp.ID)}
	require.NoError(t, s.Create(ctx, catScoped))
	require.NoError(t, s.Create(ctx, typeScoped))

	// Deleting only the type removes the type-scoped response; the category one stays.
	require.NoError(t, cs.DeleteType(ctx, tp.ID))

	got, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, catScoped.ID, got[0].ID)
}
