package category_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
)

// A blank name is a validation error on every create and update (#418), so
// the HTTP layer can answer 400 instead of 500.
func TestService_BlankNameIsErrValidation(t *testing.T) {
	ctx := context.Background()
	store := newFakeCategoryStore()
	svc := category.NewService(store)

	cat, err := svc.CreateCategory(ctx, "C", 1)
	require.NoError(t, err)
	tp, err := svc.CreateType(ctx, cat.ID, "T", 1)
	require.NoError(t, err)
	it, err := svc.CreateItem(ctx, tp.ID, "I", 1)
	require.NoError(t, err)

	for name, call := range map[string]func() error{
		"create category": func() error { _, err := svc.CreateCategory(ctx, " ", 1); return err },
		"create type":     func() error { _, err := svc.CreateType(ctx, cat.ID, " ", 1); return err },
		"create item":     func() error { _, err := svc.CreateItem(ctx, tp.ID, " ", 1); return err },
		"update category": func() error { c := cat; c.Name = " "; return svc.UpdateCategory(ctx, c) },
		"update type":     func() error { x := tp; x.Name = " "; return svc.UpdateType(ctx, x) },
		"update item":     func() error { x := it; x.Name = " "; return svc.UpdateItem(ctx, x) },
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, call(), category.ErrValidation)
		})
	}
}
