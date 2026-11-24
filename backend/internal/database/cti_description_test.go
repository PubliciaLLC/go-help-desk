package database_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// A description survives the round trip at all three levels, on both the
// single-row read and the list the forms read, through create and update. It
// is stored as given (the service has cleaned it), and an archived row keeps it.
func TestCategoryStore_DescriptionRoundTrip(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	svc := category.NewService(categorystore.New(q))

	const html = "<b>Laptops</b>\nand docks & \"monitors\""
	long := strings.Repeat("é", 500)

	cat, err := svc.CreateCategoryWithDescription(ctx, "Hardware", html, 1)
	require.NoError(t, err)
	tp, err := svc.CreateTypeWithDescription(ctx, cat.ID, "Laptop", "Type text", 1)
	require.NoError(t, err)
	it, err := svc.CreateItemWithDescription(ctx, tp.ID, "Dock", long, 1)
	require.NoError(t, err)
	// And one of each with no description, to show the default is "".
	bare, err := svc.CreateCategory(ctx, "Bare", 2)
	require.NoError(t, err)

	gotCat, err := svc.GetCategory(ctx, cat.ID)
	require.NoError(t, err)
	gotType, err := svc.GetType(ctx, tp.ID)
	require.NoError(t, err)
	gotItem, err := svc.GetItem(ctx, it.ID)
	require.NoError(t, err)
	gotBare, err := svc.GetCategory(ctx, bare.ID)
	require.NoError(t, err)
	require.Equal(t, html, gotCat.Description, "stored verbatim, newline and markup included")
	require.Equal(t, "Type text", gotType.Description)
	require.Equal(t, long, gotItem.Description)
	require.Equal(t, "", gotBare.Description)

	// The lists the ticket forms read carry it, active-only or not.
	cats, err := svc.ListCategories(ctx, true)
	require.NoError(t, err)
	require.Equal(t, html, findCategory(t, cats, cat.ID).Description)
	types, err := svc.ListTypes(ctx, cat.ID, true)
	require.NoError(t, err)
	require.Equal(t, "Type text", types[0].Description)
	items, err := svc.ListItems(ctx, tp.ID, true)
	require.NoError(t, err)
	require.Equal(t, long, items[0].Description)

	// Update changes it, and clearing it with "" leaves none.
	gotCat.Description = "  Changed  "
	require.NoError(t, svc.UpdateCategory(ctx, gotCat))
	gotType.Description = ""
	require.NoError(t, svc.UpdateType(ctx, gotType))
	gotItem.Description = "Item text"
	require.NoError(t, svc.UpdateItem(ctx, gotItem))
	gotCat, _ = svc.GetCategory(ctx, cat.ID)
	gotType, _ = svc.GetType(ctx, tp.ID)
	gotItem, _ = svc.GetItem(ctx, it.ID)
	require.Equal(t, []string{"Changed", "", "Item text"}, []string{gotCat.Description, gotType.Description, gotItem.Description})

	// Archiving keeps it: the archived list (staff see archived rows) still has it.
	gotCat.Active, gotType.Active, gotItem.Active = false, false, false
	gotType.Description = "Kept"
	require.NoError(t, svc.UpdateCategory(ctx, gotCat))
	require.NoError(t, svc.UpdateType(ctx, gotType))
	require.NoError(t, svc.UpdateItem(ctx, gotItem))
	all, err := svc.ListCategories(ctx, false)
	require.NoError(t, err)
	archived := findCategory(t, all, cat.ID)
	require.False(t, archived.Active)
	require.Equal(t, "Changed", archived.Description)
	archivedTypes, err := svc.ListTypes(ctx, cat.ID, false)
	require.NoError(t, err)
	require.Equal(t, "Kept", archivedTypes[0].Description)
	archivedItems, err := svc.ListItems(ctx, tp.ID, false)
	require.NoError(t, err)
	require.Equal(t, "Item text", archivedItems[0].Description)
}

// The CHECK backs up the service: a row that gets past it is still refused.
func TestCategoryStore_DescriptionCheckRefusesTooLong(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	err := categorystore.New(q).CreateCategory(ctx, category.Category{
		ID: uuid.New(), Name: "Too long", Description: strings.Repeat("a", 501), Active: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "categories_description_len")
}

func findCategory(t *testing.T, cats []category.Category, id uuid.UUID) category.Category {
	t.Helper()
	for _, c := range cats {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("category %x not in the list", id)
	return category.Category{}
}
