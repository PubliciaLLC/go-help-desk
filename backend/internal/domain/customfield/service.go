package customfield

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// Service manages custom field definitions, CTI assignments, and ticket values.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) CreateFieldDef(ctx context.Context, name string, fieldType FieldType, options []string, sortOrder int, actorID *uuid.UUID) (FieldDef, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return FieldDef{}, Invalid("field name is required")
	}
	if err := validateFieldType(fieldType); err != nil {
		return FieldDef{}, err
	}
	if fieldType == FieldTypeSelect && len(options) == 0 {
		return FieldDef{}, Invalid("select fields require at least one option")
	}
	def := FieldDef{
		ID:        uuid.New(),
		Name:      name,
		FieldType: fieldType,
		Options:   options,
		SortOrder: sortOrder,
		Active:    true,
	}
	return s.store.CreateFieldDef(ctx, def, entriesForCreated(actorID, def))
}

// ListFieldDefs is every definition with PlaceCount set, in field-number order.
func (s *Service) ListFieldDefs(ctx context.Context) ([]FieldDef, error) {
	return s.store.ListFieldDefs(ctx)
}

func (s *Service) GetFieldDef(ctx context.Context, id uuid.UUID) (FieldDef, error) {
	return s.store.GetFieldDef(ctx, id)
}

// FieldDefPatch is a change to a definition. A nil field is left as it is, so
// two administrators changing different fields of one definition do not undo
// each other: the patch is applied to the row as it is when the lock is taken,
// not to a copy read earlier. A non-nil, empty Options or RetiredOptions
// clears that list.
type FieldDefPatch struct {
	Name           *string
	FieldType      *FieldType
	Options        []string
	RetiredOptions []string
	SortOrder      *int
	Active         *bool
}

// PatchFieldDef applies p to the definition and audits what changed, in one
// transaction under a lock on the definition's row. Besides the shape of the
// definition it enforces what tickets make true: an option some ticket holds
// cannot be removed (retire it instead), and the type cannot change once any
// ticket holds a value. Both are Conflict.
func (s *Service) PatchFieldDef(ctx context.Context, id uuid.UUID, p FieldDefPatch, actorID *uuid.UUID) (FieldDef, error) {
	return s.change(ctx, id, actorID, func(cur FieldDef) FieldDef {
		if p.Name != nil {
			cur.Name = *p.Name
		}
		if p.FieldType != nil {
			cur.FieldType = *p.FieldType
		}
		if p.Options != nil {
			cur.Options = p.Options
		}
		if p.RetiredOptions != nil {
			cur.RetiredOptions = p.RetiredOptions
		}
		if p.SortOrder != nil {
			cur.SortOrder = *p.SortOrder
		}
		if p.Active != nil {
			cur.Active = *p.Active
		}
		return cur
	})
}

// UpdateFieldDef replaces the definition with def, under the same rules and the
// same audit as PatchFieldDef. The routes use PatchFieldDef; this is the
// whole-definition form for a caller that holds all of it.
func (s *Service) UpdateFieldDef(ctx context.Context, def FieldDef, actorID *uuid.UUID) error {
	_, err := s.change(ctx, def.ID, actorID, func(cur FieldDef) FieldDef {
		def.CreatedAt = cur.CreatedAt
		return def
	})
	return err
}

func (s *Service) change(ctx context.Context, id uuid.UUID, actor *uuid.UUID, apply func(cur FieldDef) FieldDef) (FieldDef, error) {
	return s.store.UpdateFieldDef(ctx, id, func(cur FieldDef, held Held) (FieldDef, []audit.Entry, error) {
		next := apply(cur)
		next.Name = strings.TrimSpace(next.Name)
		if err := checkChange(cur, next, held); err != nil {
			return FieldDef{}, nil, err
		}
		return next, entriesForChange(actor, cur, next), nil
	})
}

// checkChange is the rule for changing a definition from cur to next.
func checkChange(cur, next FieldDef, held Held) error {
	if next.Name == "" {
		return Invalid("field name is required")
	}
	if err := validateFieldType(next.FieldType); err != nil {
		return err
	}
	if next.FieldType == FieldTypeSelect {
		if len(next.Options) == 0 {
			return Invalid("select fields require at least one option")
		}
		for _, o := range next.RetiredOptions {
			if slices.Contains(next.Options, o) {
				return Invalid(fmt.Sprintf("option %q is both in use and retired", o))
			}
			// A value enters retired_options only from options. Retiring is a
			// move; a value nobody offered has nothing to retire, and one that
			// tickets might hold without being listed would be hidden from the
			// delete check.
			if !slices.Contains(cur.Options, o) && !slices.Contains(cur.RetiredOptions, o) {
				return Invalid(fmt.Sprintf("option %q can be retired only from the options in use", o))
			}
		}
		// A value in neither list now that was in one before is a delete.
		if cur.FieldType == FieldTypeSelect {
			var removed []string
			for _, o := range append(slices.Clone(cur.Options), cur.RetiredOptions...) {
				if !slices.Contains(next.Options, o) && !slices.Contains(next.RetiredOptions, o) {
					removed = append(removed, o)
				}
			}
			if len(removed) > 0 {
				v, found, err := held.OneOf(removed)
				if err != nil {
					return fmt.Errorf("checking whether an option is in use: %w", err)
				}
				if found {
					return Conflict(fmt.Sprintf("option %q is in use; retire it instead", v))
				}
			}
		}
	}
	if cur.FieldType != next.FieldType {
		used, err := held.Any()
		if err != nil {
			return fmt.Errorf("checking whether the field is in use: %w", err)
		}
		if used {
			return Conflict("the type can't change once a ticket has a value for this field")
		}
	}
	return nil
}

// ReorderFieldDefs sets the field numbers: position n of ids becomes number n.
// ids must name every definition exactly once, active and inactive alike. A
// repeated id is the caller's mistake (Invalid); a list that does not match the
// stored fields is stale (Conflict), because a field was added since it was
// read, and nothing is written.
func (s *Service) ReorderFieldDefs(ctx context.Context, ids []uuid.UUID, actor *uuid.UUID) error {
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return Invalid(fmt.Sprintf("field %s is listed twice", id))
		}
		seen[id] = true
	}
	return s.store.ReorderFieldDefs(ctx, func(cur []FieldDef) ([]uuid.UUID, []audit.Entry, error) {
		stale := Conflict("the field list has changed; reload and try again")
		if len(cur) != len(ids) {
			return nil, nil, stale
		}
		for _, d := range cur {
			if !seen[d.ID] {
				return nil, nil, stale
			}
		}
		return ids, entriesForReorder(actor, cur, ids), nil
	})
}

// Usage is how many tickets hold a value for the field and, for a select field,
// each of its values: the options in use, then the retired ones, then any stored
// value that is in neither list (alphabetically), each with its count, 0 for one
// no ticket holds. Other types have no values. The counts are advisory: a value
// can be written after they are read. PatchFieldDef is what enforces the rule.
func (s *Service) Usage(ctx context.Context, id uuid.UUID) (Usage, error) {
	def, err := s.store.GetFieldDef(ctx, id)
	if err != nil {
		return Usage{}, err
	}
	total, err := s.store.CountValues(ctx, id)
	if err != nil {
		return Usage{}, err
	}
	u := Usage{TicketsWithValue: total, Values: []ValueCount{}}
	if def.FieldType != FieldTypeSelect {
		return u, nil
	}
	counts, err := s.store.CountValuesByValue(ctx, id)
	if err != nil {
		return Usage{}, err
	}
	listed := make(map[string]bool, len(def.Options)+len(def.RetiredOptions))
	for _, v := range append(slices.Clone(def.Options), def.RetiredOptions...) {
		listed[v] = true
		u.Values = append(u.Values, ValueCount{Value: v, Tickets: counts[v]})
	}
	var strays []string
	for v := range counts {
		if !listed[v] {
			strays = append(strays, v)
		}
	}
	slices.Sort(strays)
	for _, v := range strays {
		u.Values = append(u.Values, ValueCount{Value: v, Tickets: counts[v]})
	}
	return u, nil
}

// ListPlacements is where the field is attached, in tree order. A field that
// does not exist is ErrNotFound; one attached nowhere is an empty list.
func (s *Service) ListPlacements(ctx context.Context, id uuid.UUID) ([]Placement, error) {
	if _, err := s.store.GetFieldDef(ctx, id); err != nil {
		return nil, err
	}
	return s.store.ListPlacements(ctx, id)
}

func (s *Service) CreateAssignment(ctx context.Context, fieldDefID uuid.UUID, scopeType ScopeType, scopeID uuid.UUID, sortOrder int, visibleOnNew, requiredOnNew bool, actor *uuid.UUID) (Assignment, error) {
	a := Assignment{
		ID:            uuid.New(),
		FieldDefID:    fieldDefID,
		ScopeType:     scopeType,
		ScopeID:       scopeID,
		SortOrder:     sortOrder,
		VisibleOnNew:  visibleOnNew,
		RequiredOnNew: requiredOnNew,
	}
	return s.store.CreateAssignment(ctx, a, func(info AssignmentInfo) audit.Entry { return entryAttached(actor, info, a) })
}

func (s *Service) ListAssignments(ctx context.Context, scopeType ScopeType, scopeID uuid.UUID) ([]Assignment, error) {
	return s.store.ListAssignments(ctx, scopeType, scopeID)
}

func (s *Service) GetAssignment(ctx context.Context, id uuid.UUID) (Assignment, error) {
	return s.store.GetAssignment(ctx, id)
}

func (s *Service) UpdateAssignment(ctx context.Context, a Assignment, actor *uuid.UUID) error {
	return s.store.UpdateAssignment(ctx, a, func(before Assignment, info AssignmentInfo) []audit.Entry {
		return entriesForAttachmentChange(actor, info, before, a)
	})
}

func (s *Service) DeleteAssignment(ctx context.Context, id uuid.UUID, actor *uuid.UUID) error {
	return s.store.DeleteAssignment(ctx, id, func(before Assignment, info AssignmentInfo) audit.Entry {
		return entryDetached(actor, info, before)
	})
}

// ResolveFieldsForCTI returns the union of field assignments for the given
// category, type, and item, one entry per field, ordered by scope level
// (category → type → item) then by the field's own number and name within each
// level (the store's order; the attachment's sort_order is not consulted). A field
// attached at more than one level is returned once, as its most specific
// attachment (item over type over category).
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

	// One entry per field. The list is built category, type, item, so a field's
	// last entry is its most specific attachment; that one's settings and level
	// apply. Filtering in place keeps the level-by-level order above.
	last := make(map[uuid.UUID]int, len(result))
	for i, a := range result {
		last[a.FieldDefID] = i
	}
	kept := result[:0]
	for i, a := range result {
		if last[a.FieldDefID] == i {
			kept = append(kept, a)
		}
	}
	return kept, nil
}

// checkValue is the rule for a write to one field, and the text to store. The
// reason is empty when the write is allowed.
//
//   - The field must be active, a clear included: checking after the empty-value
//     branch let a caller wipe the stored value of a deactivated field by
//     sending "".
//   - An empty value is a clear and is otherwise exempt.
//   - A select value must be one of the options in use. A retired option is
//     refused too, though a ticket that already holds it keeps showing it.
//   - A number field takes a number (IsNumber), stored trimmed; text that is
//     only spaces is a clear.
func checkValue(def FieldDef, value string) (stored string, reason Reason) {
	if !def.Active {
		return "", ReasonInactive
	}
	switch def.FieldType {
	case FieldTypeNumber:
		value = strings.TrimSpace(value)
		if value != "" && !IsNumber(value) {
			return "", ReasonNotANumber
		}
	case FieldTypeSelect:
		if value != "" && !slices.Contains(def.Options, value) {
			if slices.Contains(def.RetiredOptions, value) {
				return "", ReasonRetiredOption
			}
			return "", ReasonNotAnOption
		}
	}
	return value, ""
}

// refusal is checkValue's reason as the 400 a caller sees.
func refusal(def FieldDef, value string, reason Reason) error {
	switch reason {
	case ReasonInactive:
		return Invalid(fmt.Sprintf("field %q is deactivated", def.Name))
	case ReasonRetiredOption:
		return Invalid(fmt.Sprintf("value %q is a retired option for field %q", value, def.Name))
	case ReasonNotANumber:
		return Invalid(fmt.Sprintf("value %q is not a number for field %q", value, def.Name))
	}
	return Invalid(fmt.Sprintf("value %q is not a valid option for field %q", value, def.Name))
}

// SetValue upserts or deletes a custom field value on a ticket, under the rule
// in checkValue. The routes use SetValues, which also locks the definition; this
// does not.
func (s *Service) SetValue(ctx context.Context, ticketID, fieldDefID uuid.UUID, value string) error {
	def, err := s.store.GetFieldDef(ctx, fieldDefID)
	if err != nil {
		return fmt.Errorf("field not found: %w", err)
	}
	stored, reason := checkValue(def, value)
	if reason != "" {
		return refusal(def, value, reason)
	}
	if stored == "" {
		return s.store.DeleteValue(ctx, ticketID, fieldDefID)
	}
	return s.store.UpsertValue(ctx, ticketID, fieldDefID, stored)
}

// SetValues is SetValue for several fields of one ticket, all or none: every
// entry is checked under a lock on its definition before any is written, so one
// refused entry leaves the ticket as it was. Whether each field belongs to the
// ticket is the caller's to settle first; this knows only the definitions.
func (s *Service) SetValues(ctx context.Context, ticketID uuid.UUID, values map[uuid.UUID]string) error {
	if len(values) == 0 {
		return nil
	}
	return s.store.SetValues(ctx, ticketID, values, func(defs map[uuid.UUID]FieldDef) (map[uuid.UUID]string, error) {
		// Sorted so a request with two bad entries gets the same answer every
		// time, not whichever the map happened to yield first.
		write := make(map[uuid.UUID]string, len(values))
		for _, id := range sortedIDs(values) {
			def, ok := defs[id]
			if !ok {
				return nil, fmt.Errorf("field not found: %w", ErrNotFound)
			}
			stored, reason := checkValue(def, values[id])
			if reason != "" {
				return nil, refusal(def, values[id], reason)
			}
			write[id] = stored
		}
		return write, nil
	})
}

func sortedIDs[V any](m map[uuid.UUID]V) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return ids
}

// SetCreationValues writes the custom field values sent with a new ticket, which
// already exists and is never refused over them. raw is the request's map as it
// came: keys are whatever the caller sent. Every entry that cannot be written is
// reported as a Warning, sorted by field_id so a client and a test see a stable
// order; an empty value means "no value" and is not one.
//
// The fields the ticket has are resolved from its category, type and item. The
// rest is one SetValues transaction in which the entries that pass checkValue
// are written and the others become warnings. The returned error is for the
// caller to log: it means the entries could not be saved (the lookup or the
// write failed), and each non-empty one is then in the warnings as not_saved.
func (s *Service) SetCreationValues(ctx context.Context, ticketID, categoryID uuid.UUID, typeID, itemID *uuid.UUID, raw map[string]string) ([]Warning, error) {
	var keys []string // the non-empty entries, sorted
	for key, value := range raw {
		if value != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	slices.Sort(keys)

	// byField orders warnings; every return goes through it.
	byField := func(ws []Warning) []Warning {
		slices.SortFunc(ws, func(a, b Warning) int { return strings.Compare(a.FieldID, b.FieldID) })
		return ws
	}
	allNotSaved := func() []Warning {
		out := make([]Warning, len(keys))
		for i, k := range keys {
			out[i] = Warning{FieldID: k, Reason: ReasonNotSaved}
		}
		return out
	}

	resolved, err := s.ResolveFieldsForCTI(ctx, categoryID, typeID, itemID)
	if err != nil {
		return allNotSaved(), fmt.Errorf("resolving the ticket's fields: %w", err)
	}
	onTicket := make(map[uuid.UUID]bool, len(resolved))
	for _, a := range resolved {
		onTicket[a.FieldDefID] = true
	}

	var warnings []Warning
	entries := make(map[uuid.UUID]string, len(keys))
	keyOf := make(map[uuid.UUID]string, len(keys))
	for _, key := range keys {
		id, err := uuid.Parse(key)
		switch {
		case err != nil:
			warnings = append(warnings, Warning{FieldID: key, Reason: ReasonInvalidFieldID})
		case !onTicket[id]:
			warnings = append(warnings, Warning{FieldID: key, Reason: ReasonNotOnTicket})
		default:
			entries[id] = raw[key]
			keyOf[id] = key
		}
	}
	if len(entries) == 0 {
		return byField(warnings), nil
	}

	var refused []Warning
	err = s.store.SetValues(ctx, ticketID, entries, func(defs map[uuid.UUID]FieldDef) (map[uuid.UUID]string, error) {
		refused = nil // decide can run again after a retried transaction
		write := make(map[uuid.UUID]string, len(entries))
		for id, value := range entries {
			def, ok := defs[id]
			if !ok {
				refused = append(refused, Warning{FieldID: keyOf[id], Reason: ReasonNotSaved})
				continue
			}
			stored, reason := checkValue(def, value)
			if reason != "" {
				refused = append(refused, Warning{FieldID: keyOf[id], Reason: reason})
				continue
			}
			// Spaces in a number field are a clear, and a new ticket has
			// nothing to clear.
			if stored != "" {
				write[id] = stored
			}
		}
		return write, nil
	})
	if err != nil {
		// Nothing was written, whichever entries had passed.
		for _, id := range sortedIDs(entries) {
			warnings = append(warnings, Warning{FieldID: keyOf[id], Reason: ReasonNotSaved})
		}
		return byField(warnings), fmt.Errorf("saving the ticket's field values: %w", err)
	}
	return byField(append(warnings, refused...)), nil
}

func (s *Service) ListValuesForTicket(ctx context.Context, ticketID uuid.UUID) ([]TicketFieldValue, error) {
	return s.store.ListValuesForTicket(ctx, ticketID)
}

func validateFieldType(ft FieldType) error {
	switch ft {
	case FieldTypeText, FieldTypeTextarea, FieldTypeNumber, FieldTypeSelect:
		return nil
	default:
		return Invalid(fmt.Sprintf("invalid field type %q", ft))
	}
}
