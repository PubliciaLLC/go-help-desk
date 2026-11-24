package cannedresponse

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// ErrValidation wraps input-validation failures from Create/Update, so
// callers (the HTTP handler) can tell them apart from persistence errors and
// map them to 400 instead of 500.
var ErrValidation = errors.New("validation failed")

// ErrNotFound is returned when a requested canned response does not exist.
// The Postgres store wraps sql.ErrNoRows with this sentinel.
var ErrNotFound = errors.New("canned response not found")

// Service manages canned responses.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

// validateScope enforces the rule that a type-scoped response must also name a
// category. The existence of the category/type is enforced by foreign keys at
// the database layer.
func validateScope(categoryID, typeID *uuid.UUID) error {
	if typeID != nil && categoryID == nil {
		return fmt.Errorf("type scope requires a category: %w", ErrValidation)
	}
	return nil
}

// validateSortOrder rejects values that don't fit in the database's int32
// column, so an out-of-range value is a 400 rather than a silent wraparound.
func validateSortOrder(sortOrder int) error {
	if sortOrder < math.MinInt32 || sortOrder > math.MaxInt32 {
		return fmt.Errorf("sort_order out of range: %w", ErrValidation)
	}
	return nil
}

// Create validates and persists a new canned response, returning the
// as-persisted record (including the database-assigned created_at).
func (s *Service) Create(ctx context.Context, name, body string, categoryID, typeID *uuid.UUID, sortOrder int) (CannedResponse, error) {
	name = strings.TrimSpace(name)
	body = strings.TrimSpace(body)
	if name == "" {
		return CannedResponse{}, fmt.Errorf("name is required: %w", ErrValidation)
	}
	if body == "" {
		return CannedResponse{}, fmt.Errorf("body is required: %w", ErrValidation)
	}
	if err := validateScope(categoryID, typeID); err != nil {
		return CannedResponse{}, err
	}
	if err := validateSortOrder(sortOrder); err != nil {
		return CannedResponse{}, err
	}
	cr := CannedResponse{
		ID:         uuid.New(),
		Name:       name,
		Body:       body,
		CategoryID: categoryID,
		TypeID:     typeID,
		SortOrder:  sortOrder,
	}
	if err := s.store.Create(ctx, cr); err != nil {
		return CannedResponse{}, fmt.Errorf("creating canned response: %w", err)
	}
	created, err := s.store.Get(ctx, cr.ID)
	if err != nil {
		return CannedResponse{}, fmt.Errorf("reloading canned response after create: %w", err)
	}
	return created, nil
}

// Update validates and persists changes to an existing canned response,
// returning the as-persisted record. Returns ErrNotFound if id does not
// exist — an update to a nonexistent row succeeds silently at the SQL level,
// so existence is confirmed by reloading the row afterward.
func (s *Service) Update(ctx context.Context, cr CannedResponse) (CannedResponse, error) {
	cr.Name = strings.TrimSpace(cr.Name)
	cr.Body = strings.TrimSpace(cr.Body)
	if cr.Name == "" {
		return CannedResponse{}, fmt.Errorf("name is required: %w", ErrValidation)
	}
	if cr.Body == "" {
		return CannedResponse{}, fmt.Errorf("body is required: %w", ErrValidation)
	}
	if err := validateScope(cr.CategoryID, cr.TypeID); err != nil {
		return CannedResponse{}, err
	}
	if err := validateSortOrder(cr.SortOrder); err != nil {
		return CannedResponse{}, err
	}
	if err := s.store.Update(ctx, cr); err != nil {
		return CannedResponse{}, fmt.Errorf("updating canned response: %w", err)
	}
	updated, err := s.store.Get(ctx, cr.ID)
	if err != nil {
		return CannedResponse{}, fmt.Errorf("reloading canned response after update: %w", err)
	}
	return updated, nil
}

// Delete removes a canned response.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.store.Delete(ctx, id)
}

// Get returns a single canned response.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (CannedResponse, error) {
	return s.store.Get(ctx, id)
}

// List returns all canned responses, ordered by sort order then name.
func (s *Service) List(ctx context.Context) ([]CannedResponse, error) {
	all, err := s.store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing canned responses: %w", err)
	}
	sortResponses(all)
	return all, nil
}

// Available returns the responses that should surface in the reply-composer
// picker for a ticket in the given category and (optional) type, ordered by
// sort order then name.
func (s *Service) Available(ctx context.Context, categoryID uuid.UUID, typeID *uuid.UUID) ([]CannedResponse, error) {
	all, err := s.store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing canned responses: %w", err)
	}
	out := make([]CannedResponse, 0, len(all))
	for _, cr := range all {
		if available(cr, categoryID, typeID) {
			out = append(out, cr)
		}
	}
	sortResponses(out)
	return out, nil
}

func sortResponses(crs []CannedResponse) {
	sort.Slice(crs, func(i, j int) bool {
		if crs[i].SortOrder != crs[j].SortOrder {
			return crs[i].SortOrder < crs[j].SortOrder
		}
		return crs[i].Name < crs[j].Name
	})
}
