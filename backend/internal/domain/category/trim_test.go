package category_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
)

// An update trims the name as a create does, so what the handler reads back is
// what was stored.
func TestService_UpdateTrimsTheName(t *testing.T) {
	ctx := context.Background()
	svc := category.NewService(newFakeCategoryStore())
	cat, err := svc.CreateCategory(ctx, "C", 1)
	require.NoError(t, err)
	tp, err := svc.CreateType(ctx, cat.ID, "T", 1)
	require.NoError(t, err)
	it, err := svc.CreateItem(ctx, tp.ID, "I", 1)
	require.NoError(t, err)

	cat.Name, tp.Name, it.Name = "  Cat  ", "  Type ", " Item"
	require.NoError(t, svc.UpdateCategory(ctx, cat))
	require.NoError(t, svc.UpdateType(ctx, tp))
	require.NoError(t, svc.UpdateItem(ctx, it))

	gotC, err := svc.GetCategory(ctx, cat.ID)
	require.NoError(t, err)
	gotT, err := svc.GetType(ctx, tp.ID)
	require.NoError(t, err)
	gotI, err := svc.GetItem(ctx, it.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"Cat", "Type", "Item"}, []string{gotC.Name, gotT.Name, gotI.Name})
}
