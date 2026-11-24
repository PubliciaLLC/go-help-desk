package category_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/stretchr/testify/require"
)

// fakeCategoryStore is an in-memory implementation of category.Store.
type fakeCategoryStore struct {
	cats  map[uuid.UUID]category.Category
	types map[uuid.UUID]category.Type
	items map[uuid.UUID]category.Item
}

func newFakeCategoryStore() *fakeCategoryStore {
	return &fakeCategoryStore{
		cats:  make(map[uuid.UUID]category.Category),
		types: make(map[uuid.UUID]category.Type),
		items: make(map[uuid.UUID]category.Item),
	}
}

func (f *fakeCategoryStore) CreateCategory(_ context.Context, c category.Category) error {
	f.cats[c.ID] = c
	return nil
}
func (f *fakeCategoryStore) GetCategory(_ context.Context, id uuid.UUID) (category.Category, error) {
	c, ok := f.cats[id]
	if !ok {
		return category.Category{}, errors.New("category not found")
	}
	return c, nil
}
func (f *fakeCategoryStore) UpdateCategory(_ context.Context, c category.Category) error {
	f.cats[c.ID] = c
	return nil
}
func (f *fakeCategoryStore) DeleteCategory(_ context.Context, id uuid.UUID) error {
	delete(f.cats, id)
	return nil
}
func (f *fakeCategoryStore) ListCategories(_ context.Context, _ bool) ([]category.Category, error) {
	out := make([]category.Category, 0, len(f.cats))
	for _, c := range f.cats {
		out = append(out, c)
	}
	return out, nil
}
func (f *fakeCategoryStore) CreateType(_ context.Context, t category.Type) error {
	f.types[t.ID] = t
	return nil
}
func (f *fakeCategoryStore) GetType(_ context.Context, id uuid.UUID) (category.Type, error) {
	t, ok := f.types[id]
	if !ok {
		return category.Type{}, errors.New("type not found")
	}
	return t, nil
}
func (f *fakeCategoryStore) UpdateType(_ context.Context, t category.Type) error {
	f.types[t.ID] = t
	return nil
}
func (f *fakeCategoryStore) DeleteType(_ context.Context, id uuid.UUID) error {
	delete(f.types, id)
	return nil
}
func (f *fakeCategoryStore) ListTypes(_ context.Context, catID uuid.UUID, _ bool) ([]category.Type, error) {
	var out []category.Type
	for _, t := range f.types {
		if t.CategoryID == catID {
			out = append(out, t)
		}
	}
	return out, nil
}
func (f *fakeCategoryStore) CreateItem(_ context.Context, i category.Item) error {
	f.items[i.ID] = i
	return nil
}
func (f *fakeCategoryStore) GetItem(_ context.Context, id uuid.UUID) (category.Item, error) {
	i, ok := f.items[id]
	if !ok {
		return category.Item{}, errors.New("item not found")
	}
	return i, nil
}
func (f *fakeCategoryStore) UpdateItem(_ context.Context, i category.Item) error {
	f.items[i.ID] = i
	return nil
}
func (f *fakeCategoryStore) DeleteItem(_ context.Context, id uuid.UUID) error {
	delete(f.items, id)
	return nil
}
func (f *fakeCategoryStore) ListItems(_ context.Context, typeID uuid.UUID, _ bool) ([]category.Item, error) {
	var out []category.Item
	for _, i := range f.items {
		if i.TypeID == typeID {
			out = append(out, i)
		}
	}
	return out, nil
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestCategoryService_CreateCategory_Valid(t *testing.T) {
	svc := category.NewService(newFakeCategoryStore())
	c, err := svc.CreateCategory(context.Background(), "Hardware", 1)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, c.ID)
	require.Equal(t, "Hardware", c.Name)
	require.True(t, c.Active)
}

func TestCategoryService_CreateCategory_EmptyName(t *testing.T) {
	svc := category.NewService(newFakeCategoryStore())
	_, err := svc.CreateCategory(context.Background(), "  ", 1)
	require.Error(t, err)
}

func TestCategoryService_CreateType_Valid(t *testing.T) {
	svc := category.NewService(newFakeCategoryStore())
	cat, err := svc.CreateCategory(context.Background(), "Software", 1)
	require.NoError(t, err)

	tp, err := svc.CreateType(context.Background(), cat.ID, "Operating System", 1)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, tp.ID)
	require.Equal(t, cat.ID, tp.CategoryID)
}

func TestCategoryService_CreateType_EmptyName(t *testing.T) {
	svc := category.NewService(newFakeCategoryStore())
	cat, err := svc.CreateCategory(context.Background(), "Software", 1)
	require.NoError(t, err)

	_, err = svc.CreateType(context.Background(), cat.ID, "", 1)
	require.Error(t, err)
}

func TestCategoryService_CreateType_CategoryNotFound(t *testing.T) {
	svc := category.NewService(newFakeCategoryStore())
	_, err := svc.CreateType(context.Background(), uuid.New(), "OS", 1)
	require.Error(t, err)
}

func TestCategoryService_CreateItem_Valid(t *testing.T) {
	svc := category.NewService(newFakeCategoryStore())
	cat, err := svc.CreateCategory(context.Background(), "Software", 1)
	require.NoError(t, err)

	tp, err := svc.CreateType(context.Background(), cat.ID, "OS", 1)
	require.NoError(t, err)

	it, err := svc.CreateItem(context.Background(), tp.ID, "Windows 11", 1)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, it.ID)
	require.Equal(t, tp.ID, it.TypeID)
}

func TestCategoryService_CreateItem_EmptyName(t *testing.T) {
	svc := category.NewService(newFakeCategoryStore())
	cat, err := svc.CreateCategory(context.Background(), "Software", 1)
	require.NoError(t, err)

	tp, err := svc.CreateType(context.Background(), cat.ID, "OS", 1)
	require.NoError(t, err)

	_, err = svc.CreateItem(context.Background(), tp.ID, "   ", 1)
	require.Error(t, err)
}

func TestCategoryService_CreateItem_TypeNotFound(t *testing.T) {
	svc := category.NewService(newFakeCategoryStore())
	_, err := svc.CreateItem(context.Background(), uuid.New(), "Windows", 1)
	require.Error(t, err)
}
