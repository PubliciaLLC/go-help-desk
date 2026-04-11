// Package categorystore implements domain/category.Store against PostgreSQL.
package categorystore

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/dbgen"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/category"
)

// Store implements category.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

func (s *Store) CreateCategory(ctx context.Context, c category.Category) error {
	return s.q.CreateCategory(ctx, dbgen.CreateCategoryParams{
		ID:        c.ID,
		Name:      c.Name,
		SortOrder: int32(c.SortOrder),
		Active:    c.Active,
	})
}

func (s *Store) GetCategory(ctx context.Context, id uuid.UUID) (category.Category, error) {
	r, err := s.q.GetCategory(ctx, id)
	if err != nil {
		return category.Category{}, fmt.Errorf("getting category %s: %w", id, err)
	}
	return category.Category{ID: r.ID, Name: r.Name, SortOrder: int(r.SortOrder), Active: r.Active}, nil
}

func (s *Store) UpdateCategory(ctx context.Context, c category.Category) error {
	return s.q.UpdateCategory(ctx, dbgen.UpdateCategoryParams{
		ID:        c.ID,
		Name:      c.Name,
		SortOrder: int32(c.SortOrder),
		Active:    c.Active,
	})
}

func (s *Store) DeleteCategory(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteCategory(ctx, id)
}

func (s *Store) ListCategories(ctx context.Context, activeOnly bool) ([]category.Category, error) {
	rows, err := s.q.ListCategories(ctx, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("listing categories: %w", err)
	}
	out := make([]category.Category, len(rows))
	for i, r := range rows {
		out[i] = category.Category{ID: r.ID, Name: r.Name, SortOrder: int(r.SortOrder), Active: r.Active}
	}
	return out, nil
}

func (s *Store) CreateType(ctx context.Context, t category.Type) error {
	return s.q.CreateType(ctx, dbgen.CreateTypeParams{
		ID:         t.ID,
		CategoryID: t.CategoryID,
		Name:       t.Name,
		SortOrder:  int32(t.SortOrder),
		Active:     t.Active,
	})
}

func (s *Store) GetType(ctx context.Context, id uuid.UUID) (category.Type, error) {
	r, err := s.q.GetType(ctx, id)
	if err != nil {
		return category.Type{}, fmt.Errorf("getting type %s: %w", id, err)
	}
	return category.Type{ID: r.ID, CategoryID: r.CategoryID, Name: r.Name, SortOrder: int(r.SortOrder), Active: r.Active}, nil
}

func (s *Store) UpdateType(ctx context.Context, t category.Type) error {
	return s.q.UpdateType(ctx, dbgen.UpdateTypeParams{
		ID:         t.ID,
		CategoryID: t.CategoryID,
		Name:       t.Name,
		SortOrder:  int32(t.SortOrder),
		Active:     t.Active,
	})
}

func (s *Store) DeleteType(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteType(ctx, id)
}

func (s *Store) ListTypes(ctx context.Context, categoryID uuid.UUID, activeOnly bool) ([]category.Type, error) {
	rows, err := s.q.ListTypes(ctx, dbgen.ListTypesParams{CategoryID: categoryID, Column2: activeOnly})
	if err != nil {
		return nil, fmt.Errorf("listing types: %w", err)
	}
	out := make([]category.Type, len(rows))
	for i, r := range rows {
		out[i] = category.Type{ID: r.ID, CategoryID: r.CategoryID, Name: r.Name, SortOrder: int(r.SortOrder), Active: r.Active}
	}
	return out, nil
}

func (s *Store) CreateItem(ctx context.Context, i category.Item) error {
	return s.q.CreateItem(ctx, dbgen.CreateItemParams{
		ID:        i.ID,
		TypeID:    i.TypeID,
		Name:      i.Name,
		SortOrder: int32(i.SortOrder),
		Active:    i.Active,
	})
}

func (s *Store) GetItem(ctx context.Context, id uuid.UUID) (category.Item, error) {
	r, err := s.q.GetItem(ctx, id)
	if err != nil {
		return category.Item{}, fmt.Errorf("getting item %s: %w", id, err)
	}
	return category.Item{ID: r.ID, TypeID: r.TypeID, Name: r.Name, SortOrder: int(r.SortOrder), Active: r.Active}, nil
}

func (s *Store) UpdateItem(ctx context.Context, i category.Item) error {
	return s.q.UpdateItem(ctx, dbgen.UpdateItemParams{
		ID:        i.ID,
		TypeID:    i.TypeID,
		Name:      i.Name,
		SortOrder: int32(i.SortOrder),
		Active:    i.Active,
	})
}

func (s *Store) DeleteItem(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteItem(ctx, id)
}

func (s *Store) ListItems(ctx context.Context, typeID uuid.UUID, activeOnly bool) ([]category.Item, error) {
	rows, err := s.q.ListItems(ctx, dbgen.ListItemsParams{TypeID: typeID, Column2: activeOnly})
	if err != nil {
		return nil, fmt.Errorf("listing items: %w", err)
	}
	out := make([]category.Item, len(rows))
	for i, r := range rows {
		out[i] = category.Item{ID: r.ID, TypeID: r.TypeID, Name: r.Name, SortOrder: int(r.SortOrder), Active: r.Active}
	}
	return out, nil
}
