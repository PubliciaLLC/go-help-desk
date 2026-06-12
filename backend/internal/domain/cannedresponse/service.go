package cannedresponse

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Service manages canned responses.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

// validateScope enforces the rule that a type-scoped response must also name a
// category. The existence of the category/type is enforced by foreign keys at
// the database layer.
func validateScope(categoryID, typeID *uuid.UUID) error {
	if typeID != nil && categoryID == nil {
		return fmt.Errorf("type scope requires a category")
	}
	return nil
}

// Create validates and persists a new canned response.
func (s *Service) Create(ctx context.Context, name, body string, categoryID, typeID *uuid.UUID, sortOrder int) (CannedResponse, error) {
	name = strings.TrimSpace(name)
	body = strings.TrimSpace(body)
	if name == "" {
		return CannedResponse{}, fmt.Errorf("name is required")
	}
	if body == "" {
		return CannedResponse{}, fmt.Errorf("body is required")
	}
	if err := validateScope(categoryID, typeID); err != nil {
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
	return cr, nil
}

// Update validates and persists changes to an existing canned response.
func (s *Service) Update(ctx context.Context, cr CannedResponse) error {
	cr.Name = strings.TrimSpace(cr.Name)
	cr.Body = strings.TrimSpace(cr.Body)
	if cr.Name == "" {
		return fmt.Errorf("name is required")
	}
	if cr.Body == "" {
		return fmt.Errorf("body is required")
	}
	if err := validateScope(cr.CategoryID, cr.TypeID); err != nil {
		return err
	}
	return s.store.Update(ctx, cr)
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
