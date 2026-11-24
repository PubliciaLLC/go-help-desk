package category

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Service manages the CTI hierarchy.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) CreateCategory(ctx context.Context, name string, sortOrder int) (Category, error) {
	c := Category{
		ID:        uuid.New(),
		Name:      strings.TrimSpace(name),
		SortOrder: sortOrder,
		Active:    true,
	}
	if c.Name == "" {
		return Category{}, fmt.Errorf("category name is required")
	}
	if err := s.store.CreateCategory(ctx, c); err != nil {
		return Category{}, fmt.Errorf("creating category: %w", err)
	}
	return c, nil
}

func (s *Service) UpdateCategory(ctx context.Context, c Category) error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("category name is required")
	}
	return s.store.UpdateCategory(ctx, c)
}

func (s *Service) DeleteCategory(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteCategory(ctx, id)
}

func (s *Service) GetCategory(ctx context.Context, id uuid.UUID) (Category, error) {
	return s.store.GetCategory(ctx, id)
}

func (s *Service) ListCategories(ctx context.Context, activeOnly bool) ([]Category, error) {
	return s.store.ListCategories(ctx, activeOnly)
}

func (s *Service) CreateType(ctx context.Context, categoryID uuid.UUID, name string, sortOrder int) (Type, error) {
	// Verify the category exists.
	if _, err := s.store.GetCategory(ctx, categoryID); err != nil {
		return Type{}, fmt.Errorf("category not found: %w", err)
	}
	tp := Type{
		ID:         uuid.New(),
		CategoryID: categoryID,
		Name:       strings.TrimSpace(name),
		SortOrder:  sortOrder,
		Active:     true,
	}
	if tp.Name == "" {
		return Type{}, fmt.Errorf("type name is required")
	}
	if err := s.store.CreateType(ctx, tp); err != nil {
		return Type{}, fmt.Errorf("creating type: %w", err)
	}
	return tp, nil
}

func (s *Service) UpdateType(ctx context.Context, tp Type) error {
	if strings.TrimSpace(tp.Name) == "" {
		return fmt.Errorf("type name is required")
	}
	return s.store.UpdateType(ctx, tp)
}

func (s *Service) DeleteType(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteType(ctx, id)
}

func (s *Service) GetType(ctx context.Context, id uuid.UUID) (Type, error) {
	return s.store.GetType(ctx, id)
}

func (s *Service) ListTypes(ctx context.Context, categoryID uuid.UUID, activeOnly bool) ([]Type, error) {
	return s.store.ListTypes(ctx, categoryID, activeOnly)
}

func (s *Service) CreateItem(ctx context.Context, typeID uuid.UUID, name string, sortOrder int) (Item, error) {
	if _, err := s.store.GetType(ctx, typeID); err != nil {
		return Item{}, fmt.Errorf("type not found: %w", err)
	}
	it := Item{
		ID:        uuid.New(),
		TypeID:    typeID,
		Name:      strings.TrimSpace(name),
		SortOrder: sortOrder,
		Active:    true,
	}
	if it.Name == "" {
		return Item{}, fmt.Errorf("item name is required")
	}
	if err := s.store.CreateItem(ctx, it); err != nil {
		return Item{}, fmt.Errorf("creating item: %w", err)
	}
	return it, nil
}

func (s *Service) UpdateItem(ctx context.Context, it Item) error {
	if strings.TrimSpace(it.Name) == "" {
		return fmt.Errorf("item name is required")
	}
	return s.store.UpdateItem(ctx, it)
}

func (s *Service) DeleteItem(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteItem(ctx, id)
}

func (s *Service) GetItem(ctx context.Context, id uuid.UUID) (Item, error) {
	return s.store.GetItem(ctx, id)
}

func (s *Service) ListItems(ctx context.Context, typeID uuid.UUID, activeOnly bool) ([]Item, error) {
	return s.store.ListItems(ctx, typeID, activeOnly)
}

// unused import prevention
var _ = time.Now
