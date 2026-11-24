package category

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaxDescription is the longest description, in characters (not bytes). The
// database holds the same limit in a CHECK (migration 000069); this is the
// check that answers with a message.
const MaxDescription = 500

// cleanDescription trims a description and refuses one that is too long or
// holds a NUL, which text columns cannot store. Empty means none. The text is
// otherwise kept verbatim: it is plain text, never parsed.
func cleanDescription(d string) (string, error) {
	d = strings.TrimSpace(d)
	if n := utf8.RuneCountInString(d); n > MaxDescription {
		return "", fmt.Errorf("description is too long: %d characters, the limit is %d: %w", n, MaxDescription, ErrValidation)
	}
	if strings.ContainsRune(d, 0) {
		return "", fmt.Errorf("description must not contain a NUL character: %w", ErrValidation)
	}
	return d, nil
}

// Service manages the CTI hierarchy.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) CreateCategory(ctx context.Context, name string, sortOrder int) (Category, error) {
	return s.CreateCategoryWithDescription(ctx, name, "", sortOrder)
}

// CreateCategoryWithDescription is CreateCategory with the optional description
// (see Description). It is a method of its own, not a new parameter, so the
// callers that have no description (setup, most tests) stay as they are.
func (s *Service) CreateCategoryWithDescription(ctx context.Context, name, description string, sortOrder int) (Category, error) {
	description, err := cleanDescription(description)
	if err != nil {
		return Category{}, err
	}
	c := Category{
		ID:          uuid.New(),
		Name:        strings.TrimSpace(name),
		Description: description,
		SortOrder:   sortOrder,
		Active:      true,
	}
	if c.Name == "" {
		return Category{}, fmt.Errorf("category name: %w", ErrValidation)
	}
	if err := s.store.CreateCategory(ctx, c); err != nil {
		return Category{}, fmt.Errorf("creating category: %w", err)
	}
	return c, nil
}

func (s *Service) UpdateCategory(ctx context.Context, c Category) error {
	// Trimmed on update as on create, so the stored name is the one the
	// handler reads back.
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return fmt.Errorf("category name: %w", ErrValidation)
	}
	var err error
	if c.Description, err = cleanDescription(c.Description); err != nil {
		return err
	}
	return s.store.UpdateCategory(ctx, c)
}

func (s *Service) DeleteCategory(ctx context.Context, id uuid.UUID, actorID *uuid.UUID) error {
	return s.store.DeleteCategory(ctx, id, actorID)
}

func (s *Service) GetCategory(ctx context.Context, id uuid.UUID) (Category, error) {
	return s.store.GetCategory(ctx, id)
}

func (s *Service) ListCategories(ctx context.Context, activeOnly bool) ([]Category, error) {
	return s.store.ListCategories(ctx, activeOnly)
}

func (s *Service) CreateType(ctx context.Context, categoryID uuid.UUID, name string, sortOrder int) (Type, error) {
	return s.CreateTypeWithDescription(ctx, categoryID, name, "", sortOrder)
}

// CreateTypeWithDescription is CreateType with the optional description.
func (s *Service) CreateTypeWithDescription(ctx context.Context, categoryID uuid.UUID, name, description string, sortOrder int) (Type, error) {
	// Verify the category exists.
	if _, err := s.store.GetCategory(ctx, categoryID); err != nil {
		return Type{}, fmt.Errorf("category not found: %w", err)
	}
	description, err := cleanDescription(description)
	if err != nil {
		return Type{}, err
	}
	tp := Type{
		ID:          uuid.New(),
		CategoryID:  categoryID,
		Name:        strings.TrimSpace(name),
		Description: description,
		SortOrder:   sortOrder,
		Active:      true,
	}
	if tp.Name == "" {
		return Type{}, fmt.Errorf("type name: %w", ErrValidation)
	}
	if err := s.store.CreateType(ctx, tp); err != nil {
		return Type{}, fmt.Errorf("creating type: %w", err)
	}
	return tp, nil
}

func (s *Service) UpdateType(ctx context.Context, tp Type) error {
	// Trimmed on update as on create, so the stored name is the one the
	// handler reads back.
	tp.Name = strings.TrimSpace(tp.Name)
	if tp.Name == "" {
		return fmt.Errorf("type name: %w", ErrValidation)
	}
	var err error
	if tp.Description, err = cleanDescription(tp.Description); err != nil {
		return err
	}
	return s.store.UpdateType(ctx, tp)
}

func (s *Service) DeleteType(ctx context.Context, id uuid.UUID, actorID *uuid.UUID) error {
	return s.store.DeleteType(ctx, id, actorID)
}

func (s *Service) GetType(ctx context.Context, id uuid.UUID) (Type, error) {
	return s.store.GetType(ctx, id)
}

func (s *Service) ListTypes(ctx context.Context, categoryID uuid.UUID, activeOnly bool) ([]Type, error) {
	return s.store.ListTypes(ctx, categoryID, activeOnly)
}

func (s *Service) CreateItem(ctx context.Context, typeID uuid.UUID, name string, sortOrder int) (Item, error) {
	return s.CreateItemWithDescription(ctx, typeID, name, "", sortOrder)
}

// CreateItemWithDescription is CreateItem with the optional description.
func (s *Service) CreateItemWithDescription(ctx context.Context, typeID uuid.UUID, name, description string, sortOrder int) (Item, error) {
	if _, err := s.store.GetType(ctx, typeID); err != nil {
		return Item{}, fmt.Errorf("type not found: %w", err)
	}
	description, err := cleanDescription(description)
	if err != nil {
		return Item{}, err
	}
	it := Item{
		ID:          uuid.New(),
		TypeID:      typeID,
		Name:        strings.TrimSpace(name),
		Description: description,
		SortOrder:   sortOrder,
		Active:      true,
	}
	if it.Name == "" {
		return Item{}, fmt.Errorf("item name: %w", ErrValidation)
	}
	if err := s.store.CreateItem(ctx, it); err != nil {
		return Item{}, fmt.Errorf("creating item: %w", err)
	}
	return it, nil
}

func (s *Service) UpdateItem(ctx context.Context, it Item) error {
	// Trimmed on update as on create, so the stored name is the one the
	// handler reads back.
	it.Name = strings.TrimSpace(it.Name)
	if it.Name == "" {
		return fmt.Errorf("item name: %w", ErrValidation)
	}
	var err error
	if it.Description, err = cleanDescription(it.Description); err != nil {
		return err
	}
	return s.store.UpdateItem(ctx, it)
}

func (s *Service) DeleteItem(ctx context.Context, id uuid.UUID, actorID *uuid.UUID) error {
	return s.store.DeleteItem(ctx, id, actorID)
}

func (s *Service) GetItem(ctx context.Context, id uuid.UUID) (Item, error) {
	return s.store.GetItem(ctx, id)
}

func (s *Service) ListItems(ctx context.Context, typeID uuid.UUID, activeOnly bool) ([]Item, error) {
	return s.store.ListItems(ctx, typeID, activeOnly)
}

// CategoryNames, TypeNames and ItemNames name many ids with one statement each.
// An id that no longer exists is absent from the map, which is how a caller
// tells a deleted entry from one that was found.
func (s *Service) CategoryNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	return s.store.CategoryNames(ctx, ids)
}

func (s *Service) TypeNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	return s.store.TypeNames(ctx, ids)
}

func (s *Service) ItemNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	return s.store.ItemNames(ctx, ids)
}

// unused import prevention
var _ = time.Now
