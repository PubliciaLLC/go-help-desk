package category

import (
	"context"

	"github.com/google/uuid"
)

// Store is the persistence interface for the CTI hierarchy.
type Store interface {
	// Categories
	CreateCategory(ctx context.Context, c Category) error
	GetCategory(ctx context.Context, id uuid.UUID) (Category, error)
	UpdateCategory(ctx context.Context, c Category) error
	DeleteCategory(ctx context.Context, id uuid.UUID) error
	ListCategories(ctx context.Context, activeOnly bool) ([]Category, error)

	// Types
	CreateType(ctx context.Context, t Type) error
	GetType(ctx context.Context, id uuid.UUID) (Type, error)
	UpdateType(ctx context.Context, t Type) error
	DeleteType(ctx context.Context, id uuid.UUID) error
	ListTypes(ctx context.Context, categoryID uuid.UUID, activeOnly bool) ([]Type, error)

	// Items
	CreateItem(ctx context.Context, i Item) error
	GetItem(ctx context.Context, id uuid.UUID) (Item, error)
	UpdateItem(ctx context.Context, i Item) error
	DeleteItem(ctx context.Context, id uuid.UUID) error
	ListItems(ctx context.Context, typeID uuid.UUID, activeOnly bool) ([]Item, error)
}
