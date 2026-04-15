package customfield

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// Service manages custom field definitions, CTI assignments, and ticket values.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) CreateFieldDef(ctx context.Context, name string, fieldType FieldType, options []string, sortOrder int) (FieldDef, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return FieldDef{}, fmt.Errorf("field name is required")
	}
	if err := validateFieldType(fieldType); err != nil {
		return FieldDef{}, err
	}
	if fieldType == FieldTypeSelect && len(options) == 0 {
		return FieldDef{}, fmt.Errorf("select fields require at least one option")
	}
	def := FieldDef{
		ID:        uuid.New(),
		Name:      name,
		FieldType: fieldType,
		Options:   options,
		SortOrder: sortOrder,
		Active:    true,
	}
	return s.store.CreateFieldDef(ctx, def)
}

func (s *Service) ListFieldDefs(ctx context.Context) ([]FieldDef, error) {
	return s.store.ListFieldDefs(ctx)
}

func (s *Service) GetFieldDef(ctx context.Context, id uuid.UUID) (FieldDef, error) {
	return s.store.GetFieldDef(ctx, id)
}

func (s *Service) UpdateFieldDef(ctx context.Context, def FieldDef) error {
	def.Name = strings.TrimSpace(def.Name)
	if def.Name == "" {
		return fmt.Errorf("field name is required")
	}
	if err := validateFieldType(def.FieldType); err != nil {
		return err
	}
	if def.FieldType == FieldTypeSelect && len(def.Options) == 0 {
		return fmt.Errorf("select fields require at least one option")
	}
	return s.store.UpdateFieldDef(ctx, def)
}

func (s *Service) CreateAssignment(ctx context.Context, fieldDefID uuid.UUID, scopeType ScopeType, scopeID uuid.UUID, sortOrder int, visibleOnNew, requiredOnNew bool) (Assignment, error) {
	a := Assignment{
		ID:            uuid.New(),
		FieldDefID:    fieldDefID,
		ScopeType:     scopeType,
		ScopeID:       scopeID,
		SortOrder:     sortOrder,
		VisibleOnNew:  visibleOnNew,
		RequiredOnNew: requiredOnNew,
	}
	return s.store.CreateAssignment(ctx, a)
}

func (s *Service) ListAssignments(ctx context.Context, scopeType ScopeType, scopeID uuid.UUID) ([]Assignment, error) {
	return s.store.ListAssignments(ctx, scopeType, scopeID)
}

func (s *Service) GetAssignment(ctx context.Context, id uuid.UUID) (Assignment, error) {
	return s.store.GetAssignment(ctx, id)
}

func (s *Service) UpdateAssignment(ctx context.Context, a Assignment) error {
	return s.store.UpdateAssignment(ctx, a)
}

func (s *Service) DeleteAssignment(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteAssignment(ctx, id)
}

// ResolveFieldsForCTI returns the union of field assignments for the given
// category, type, and item, ordered by scope level (category → type → item)
// then by sort_order within each level.
func (s *Service) ResolveFieldsForCTI(ctx context.Context, categoryID uuid.UUID, typeID, itemID *uuid.UUID) ([]Assignment, error) {
	var result []Assignment

	catFields, err := s.store.ListAssignments(ctx, CategoryScope, categoryID)
	if err != nil {
		return nil, fmt.Errorf("resolving category fields: %w", err)
	}
	result = append(result, catFields...)

	if typeID != nil {
		typeFields, err := s.store.ListAssignments(ctx, TypeScope, *typeID)
		if err != nil {
			return nil, fmt.Errorf("resolving type fields: %w", err)
		}
		result = append(result, typeFields...)
	}

	if itemID != nil {
		itemFields, err := s.store.ListAssignments(ctx, ItemScope, *itemID)
		if err != nil {
			return nil, fmt.Errorf("resolving item fields: %w", err)
		}
		result = append(result, itemFields...)
	}

	return result, nil
}

// SetValue upserts or deletes a custom field value on a ticket.
// An empty value string deletes the row. Validates that the field is active
// and that select values are within the allowed options.
func (s *Service) SetValue(ctx context.Context, ticketID, fieldDefID uuid.UUID, value string) error {
	if value == "" {
		return s.store.DeleteValue(ctx, ticketID, fieldDefID)
	}
	def, err := s.store.GetFieldDef(ctx, fieldDefID)
	if err != nil {
		return fmt.Errorf("field not found: %w", err)
	}
	if !def.Active {
		return fmt.Errorf("field %q is deactivated", def.Name)
	}
	if def.FieldType == FieldTypeSelect && !slices.Contains(def.Options, value) {
		return fmt.Errorf("value %q is not a valid option for field %q", value, def.Name)
	}
	return s.store.UpsertValue(ctx, ticketID, fieldDefID, value)
}

func (s *Service) ListValuesForTicket(ctx context.Context, ticketID uuid.UUID) ([]TicketFieldValue, error) {
	return s.store.ListValuesForTicket(ctx, ticketID)
}

func validateFieldType(ft FieldType) error {
	switch ft {
	case FieldTypeText, FieldTypeTextarea, FieldTypeNumber, FieldTypeSelect:
		return nil
	default:
		return fmt.Errorf("invalid field type %q", ft)
	}
}
