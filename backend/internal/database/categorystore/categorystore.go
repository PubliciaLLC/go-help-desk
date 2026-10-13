// Package categorystore implements domain/category.Store against PostgreSQL.
package categorystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
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
		if errors.Is(err, sql.ErrNoRows) {
			return category.Category{}, fmt.Errorf("category %s: %w", id, category.ErrNotFound)
		}
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

// DeleteCategory refuses a category that tickets or SLA records still depend
// on. Counted first so the ordinary refusal never issues a failing statement
// (which would abort an enclosing transaction) and can say how many. The SLA
// records count because the delete cascades to the category's SLA policies,
// and sla_records.policy_id is RESTRICT. A ticket filed between the counts and
// the delete still trips the foreign key; that is mapped to the same sentinel.
func (s *Store) DeleteCategory(ctx context.Context, id uuid.UUID) error {
	tickets, err := s.q.CountTicketsByCategory(ctx, id)
	if err != nil {
		return fmt.Errorf("counting tickets for category %s: %w", id, err)
	}
	records, err := s.q.CountSLARecordsByCategory(ctx, database.NullUUID(&id))
	if err != nil {
		return fmt.Errorf("counting SLA records for category %s: %w", id, err)
	}
	if tickets > 0 || records > 0 {
		return fmt.Errorf("%w: it is used by %d ticket(s) and %d SLA record(s)", category.ErrCategoryInUse, tickets, records)
	}
	n, err := s.q.DeleteCategory(ctx, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("%w: a ticket was filed under it while it was being deleted", category.ErrCategoryInUse)
		}
		return fmt.Errorf("deleting category %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("category %s: %w", id, category.ErrNotFound)
	}
	return nil
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
	err := s.q.CreateType(ctx, dbgen.CreateTypeParams{
		ID:         t.ID,
		CategoryID: t.CategoryID,
		Name:       t.Name,
		SortOrder:  int32(t.SortOrder),
		Active:     t.Active,
	})
	if isFKViolationOn(err, "types_category_id_fkey") {
		return fmt.Errorf("category %s: %w", t.CategoryID, category.ErrNotFound)
	}
	return err
}

func (s *Store) GetType(ctx context.Context, id uuid.UUID) (category.Type, error) {
	r, err := s.q.GetType(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return category.Type{}, fmt.Errorf("type %s: %w", id, category.ErrNotFound)
		}
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

// DeleteType refuses a type that tickets use. A ticket holding one of its
// items holds the type too, so the one count covers the cascade to items.
func (s *Store) DeleteType(ctx context.Context, id uuid.UUID) error {
	tickets, err := s.q.CountTicketsByType(ctx, database.NullUUID(&id))
	if err != nil {
		return fmt.Errorf("counting tickets for type %s: %w", id, err)
	}
	if tickets > 0 {
		return fmt.Errorf("%w: it is used by %d ticket(s)", category.ErrTypeInUse, tickets)
	}
	n, err := s.q.DeleteType(ctx, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("%w: a ticket was filed under it while it was being deleted", category.ErrTypeInUse)
		}
		return fmt.Errorf("deleting type %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("type %s: %w", id, category.ErrNotFound)
	}
	return nil
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
	err := s.q.CreateItem(ctx, dbgen.CreateItemParams{
		ID:        i.ID,
		TypeID:    i.TypeID,
		Name:      i.Name,
		SortOrder: int32(i.SortOrder),
		Active:    i.Active,
	})
	if isFKViolationOn(err, "items_type_id_fkey") {
		return fmt.Errorf("type %s: %w", i.TypeID, category.ErrNotFound)
	}
	return err
}

func (s *Store) GetItem(ctx context.Context, id uuid.UUID) (category.Item, error) {
	r, err := s.q.GetItem(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return category.Item{}, fmt.Errorf("item %s: %w", id, category.ErrNotFound)
		}
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
	tickets, err := s.q.CountTicketsByItem(ctx, database.NullUUID(&id))
	if err != nil {
		return fmt.Errorf("counting tickets for item %s: %w", id, err)
	}
	if tickets > 0 {
		return fmt.Errorf("%w: it is used by %d ticket(s)", category.ErrItemInUse, tickets)
	}
	n, err := s.q.DeleteItem(ctx, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("%w: a ticket was filed under it while it was being deleted", category.ErrItemInUse)
		}
		return fmt.Errorf("deleting item %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("item %s: %w", id, category.ErrNotFound)
	}
	return nil
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

// Detected off the typed Postgres error and the constraint name, never the
// message text.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func isFKViolationOn(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == constraint
}
