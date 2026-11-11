package customfield

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// Service manages custom field definitions, CTI assignments, and ticket values.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) CreateFieldDef(ctx context.Context, name string, fieldType FieldType, options []string, sortOrder int, actorID *uuid.UUID) (FieldDef, error) {
	return s.CreateFieldDefLimited(ctx, name, fieldType, options, sortOrder, nil, actorID)
}

// CreateFieldDefLimited is CreateFieldDef with the field's own limit on a value
// (see FieldDef.MaxLength).
func (s *Service) CreateFieldDefLimited(ctx context.Context, name string, fieldType FieldType, options []string, sortOrder int, maxLength *int, actorID *uuid.UUID) (FieldDef, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return FieldDef{}, Invalid("field name is required")
	}
	if err := validateFieldType(fieldType); err != nil {
		return FieldDef{}, err
	}
	if fieldType == FieldTypeSelect {
		if len(options) == 0 {
			return FieldDef{}, Invalid("select fields require at least one option")
		}
		if err := checkOptionLists(options, nil); err != nil {
			return FieldDef{}, err
		}
	}
	if err := checkMaxLength(fieldType, maxLength); err != nil {
		return FieldDef{}, err
	}
	def := FieldDef{
		ID:        uuid.New(),
		Name:      name,
		FieldType: fieldType,
		Options:   options,
		SortOrder: sortOrder,
		Active:    true,
		MaxLength: maxLength,
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
	// MaxLength sets the field's own limit; ClearMaxLength removes it. Neither
	// leaves it as it is.
	MaxLength      *int
	ClearMaxLength bool
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
		if p.ClearMaxLength {
			cur.MaxLength = nil
		} else if p.MaxLength != nil {
			cur.MaxLength = p.MaxLength
		}
		return cur
	})
}

// UpdateFieldDef replaces the definition with def, under the same rules and the
// same audit as PatchFieldDef.
//
// Deprecated: the routes use PatchFieldDef, which applies a patch to the row as
// it is under the lock; this takes a whole definition read earlier. It stays
// because tests call it.
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
	// Checked on every change, since a change of type can leave the limit above
	// the new type's. Lowering it below values that exist is fine: they stay.
	if err := checkMaxLength(next.FieldType, next.MaxLength); err != nil {
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
		// Only when this change touches the lists (or makes the field a select),
		// so a field that 1.2.0 or an earlier API call left with a blank or
		// repeated option can still be renamed or reordered.
		if !slices.Equal(cur.Options, next.Options) || !slices.Equal(cur.RetiredOptions, next.RetiredOptions) || cur.FieldType != next.FieldType {
			if err := checkOptionLists(next.Options, next.RetiredOptions); err != nil {
				return err
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

// checkMaxLength is the rule for a field's own limit: only a text or long-text
// field has one, and it is at least 1 and lower than the type's limit, so it can
// only narrow what the type takes. Nil is no limit of its own.
func checkMaxLength(ft FieldType, own *int) error {
	if own == nil {
		return nil
	}
	if ft != FieldTypeText && ft != FieldTypeTextarea {
		return Invalid("only a text or long text field can have a maximum length")
	}
	if cap := MaxChars(ft); *own < 1 || *own >= cap {
		return Invalid(fmt.Sprintf("the maximum length must be between 1 and %d characters, less than the %d a field of this type takes", cap-1, cap))
	}
	return nil
}

// checkOptionLists refuses a blank option and an option that appears twice
// across both lists, compared exactly (so "a" and "A" are two options, as the
// ticket value check treats them). The page has always refused both; the API
// did not (#626).
func checkOptionLists(options, retired []string) error {
	seen := make(map[string]bool, len(options)+len(retired))
	for _, o := range append(slices.Clone(options), retired...) {
		if strings.TrimSpace(o) == "" {
			return Invalid("an option cannot be blank")
		}
		// An option longer than a select value may be could never be chosen.
		if utf8.RuneCountInString(o) > MaxTextChars {
			return Invalid(fmt.Sprintf("an option is at most %d characters", MaxTextChars))
		}
		if seen[o] {
			return Invalid(fmt.Sprintf("option %q is listed twice", o))
		}
		seen[o] = true
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

// CreateAssignment attaches a field to a node without offering it to guests.
// Attach takes the whole attachment.
func (s *Service) CreateAssignment(ctx context.Context, fieldDefID uuid.UUID, scopeType ScopeType, scopeID uuid.UUID, sortOrder int, visibleOnNew, requiredOnNew bool, actor *uuid.UUID) (Assignment, error) {
	return s.Attach(ctx, Assignment{
		FieldDefID:    fieldDefID,
		ScopeType:     scopeType,
		ScopeID:       scopeID,
		SortOrder:     sortOrder,
		VisibleOnNew:  visibleOnNew,
		RequiredOnNew: requiredOnNew,
	}, actor)
}

// Attach attaches a field to a node. The id is assigned here.
func (s *Service) Attach(ctx context.Context, a Assignment, actor *uuid.UUID) (Assignment, error) {
	if err := checkGuestScope(a); err != nil {
		return Assignment{}, err
	}
	a.ID = uuid.New()
	return s.store.CreateAssignment(ctx, a, func(info AssignmentInfo) audit.Entry { return entryAttached(actor, info, a) })
}

// checkGuestScope: a guest chooses a category and nothing below it, so only a
// category's attachment can be offered to one. A flag on a type or an item would
// promise a field no guest can ever be given.
func checkGuestScope(a Assignment) error {
	if a.VisibleToGuests && a.ScopeType != CategoryScope {
		return Invalid("only a field attached to a category can be offered to guests")
	}
	return nil
}

func (s *Service) ListAssignments(ctx context.Context, scopeType ScopeType, scopeID uuid.UUID) ([]Assignment, error) {
	return s.store.ListAssignments(ctx, scopeType, scopeID)
}

func (s *Service) GetAssignment(ctx context.Context, id uuid.UUID) (Assignment, error) {
	return s.store.GetAssignment(ctx, id)
}

func (s *Service) UpdateAssignment(ctx context.Context, a Assignment, actor *uuid.UUID) error {
	if err := checkGuestScope(a); err != nil {
		return err
	}
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
//   - A value is at most MaxChars(type) characters, checked before the type's
//     own rule so that a refusal never has a long value to echo. This is the one
//     place every door's check ends up (the rest of this package, the PUT route,
//     creation, the guest form, MCP), so none of them can store what the index
//     under the values would refuse (a write that failed for length lost the
//     whole request's values, a required one included). A value already stored
//     over the limit is left alone and is refused only if someone writes it again.
func checkValue(def FieldDef, value string) (stored string, reason Reason) {
	if !def.Active {
		return "", ReasonInactive
	}
	if def.FieldType == FieldTypeNumber {
		value = strings.TrimSpace(value)
	}
	if tooLong(def, value) {
		return "", ReasonTooLong
	}
	return checkRest(def, value)
}

// checkRest is checkValue after the length: what the type itself requires.
func checkRest(def FieldDef, value string) (stored string, reason Reason) {
	switch def.FieldType {
	case FieldTypeNumber:
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

// tooLong is whether value is over the field's limit, in characters. A number is
// counted trimmed, as it is stored.
func tooLong(def FieldDef, value string) bool {
	if def.FieldType == FieldTypeNumber {
		value = strings.TrimSpace(value)
	}
	// A byte count at or under the limit is under it in characters too, which
	// spares counting the runes of every ordinary value.
	max := def.EffectiveMaxLength()
	return len(value) > max && utf8.RuneCountInString(value) > max
}

// refusalFor is refusal in the words for the caller. A guest is told a retired
// option is "not a valid option", the same as for one that never existed: the
// guest form is never shown retired options (GET /guest/fields leaves them out),
// so a different message would let a visitor confirm a guess about one.
func refusalFor(def FieldDef, value string, reason Reason, guest bool) error {
	if guest && reason == ReasonRetiredOption {
		reason = ReasonNotAnOption
	}
	return refusal(def, value, reason)
}

// refusal is checkValue's reason as the 400 a caller sees.
func refusal(def FieldDef, value string, reason Reason) error {
	switch reason {
	case ReasonInactive:
		return Invalid(fmt.Sprintf("field %q is deactivated", def.Name))
	case ReasonTooLong:
		// Not echoed: it can be ten thousand characters.
		return Invalid(fmt.Sprintf("value for field %q is too long: at most %d characters", def.Name, def.EffectiveMaxLength()))
	case ReasonRetiredOption:
		return Invalid(fmt.Sprintf("value %q is a retired option for field %q", value, def.Name))
	case ReasonNotANumber:
		return Invalid(fmt.Sprintf("value %q is not a number for field %q", value, def.Name))
	}
	return Invalid(fmt.Sprintf("value %q is not a valid option for field %q", value, def.Name))
}

// SetValue is SetValues for one field, so it takes the same lock on the
// definition: a write that skipped it could land between an admin change's check
// and its commit.
//
// Deprecated: nothing in production calls it any more; the routes write through
// SetValues and creation through SetCreationValues. It stays because tests call
// it, and CLAUDE.md asks that nothing be removed without a deprecation first.
func (s *Service) SetValue(ctx context.Context, ticketID, fieldDefID uuid.UUID, value string) error {
	return s.SetValues(ctx, ticketID, map[uuid.UUID]string{fieldDefID: value})
}

// SetValues is SetValue for several fields of one ticket, all or none: every
// entry is checked under a lock on its definition before any is written, so one
// refused entry leaves the ticket as it was. Whether each field belongs to the
// ticket is the caller's to settle first; this knows only the definitions.
func (s *Service) SetValues(ctx context.Context, ticketID uuid.UUID, values map[uuid.UUID]string) error {
	if len(values) == 0 {
		return nil
	}
	// What the ticket holds, so that a value sent back unchanged is not held to
	// a limit lowered since (see below). Read outside the lock: it only ever
	// spares a check, and a value written in between is a different value.
	held, err := s.heldValues(ctx, ticketID)
	if err != nil {
		return err
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
			if reason == ReasonTooLong {
				// Lowering a field's limit leaves the values that exist, and a
				// value that has not changed is not checked again, so a save that
				// sends it back (an API client that sends every field) is not
				// refused over a limit it never chose. It is not rewritten either:
				// a value over the type's own limit could not be, and nothing
				// changed. A different value is held to the limit as usual.
				if held[id] == values[id] {
					if _, reason = checkRest(def, values[id]); reason == "" {
						continue
					}
				}
			}
			if reason != "" {
				return nil, refusal(def, values[id], reason)
			}
			write[id] = stored
		}
		return write, nil
	})
}

// heldValues is the values the ticket holds, by field.
func (s *Service) heldValues(ctx context.Context, ticketID uuid.UUID) (map[uuid.UUID]string, error) {
	held, err := s.store.ListValuesForTicket(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("reading the values the ticket holds: %w", err)
	}
	out := make(map[uuid.UUID]string, len(held))
	for _, v := range held {
		out[v.FieldDefID] = v.Value
	}
	return out, nil
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
	return s.setCreationValues(ctx, ticketID, raw, func() ([]Assignment, error) {
		return s.ResolveFieldsForCTI(ctx, categoryID, typeID, itemID)
	})
}

// SetGuestCreationValues is SetCreationValues for a ticket a guest filed: the
// fields it may hold are the ones offered to guests, not everything its category
// has, so a value for any other field is dropped and reported as not_on_ticket
// even if the request check let it through.
func (s *Service) SetGuestCreationValues(ctx context.Context, ticketID, categoryID uuid.UUID, raw map[string]string) ([]Warning, error) {
	return s.setCreationValues(ctx, ticketID, raw, func() ([]Assignment, error) {
		return s.GuestFields(ctx, categoryID)
	})
}

// setCreationValues is the body of both. fields says which fields the ticket
// may hold, and is not asked when nothing was sent.
func (s *Service) setCreationValues(ctx context.Context, ticketID uuid.UUID, raw map[string]string, fields func() ([]Assignment, error)) ([]Warning, error) {
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

	resolved, err := fields()
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
	// Two keys can spell one id (case, braces). Which value to keep would be luck,
	// so neither is written, and each is reported; PUT refuses the request for the
	// same thing.
	spelled := make(map[uuid.UUID][]string, len(keys))
	for _, key := range keys {
		id, err := uuid.Parse(key)
		if err != nil {
			warnings = append(warnings, Warning{FieldID: key, Reason: ReasonInvalidFieldID})
			continue
		}
		spelled[id] = append(spelled[id], key)
	}
	for id, ks := range spelled {
		switch {
		case len(ks) > 1:
			for _, key := range ks {
				warnings = append(warnings, Warning{FieldID: key, Reason: ReasonDuplicateFieldID})
			}
		case !onTicket[id]:
			warnings = append(warnings, Warning{FieldID: ks[0], Reason: ReasonNotOnTicket})
		default:
			entries[id] = raw[ks[0]]
			keyOf[id] = ks[0]
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

// valuesByID reads the request's map by field id. Keys that are not ids are not
// in it (SetCreationValues reports them); two keys that spell one id are in dup.
func valuesByID(raw map[string]string) (values map[uuid.UUID]string, dup map[uuid.UUID]bool) {
	values, dup = make(map[uuid.UUID]string, len(raw)), map[uuid.UUID]bool{}
	for key, v := range raw {
		id, err := uuid.Parse(key)
		if err != nil {
			continue
		}
		if _, seen := values[id]; seen {
			dup[id] = true
		}
		values[id] = v
	}
	return values, dup
}

// checkRequired is the rule for the fields a door offers: each one the form
// would hold a person to (shown, required, active, at its most specific
// attachment, which is what offered holds) needs a value, and the value has to
// be one the field accepts, because a value refused after the ticket exists
// would leave it without the field this was checking for. Spaces are no value.
//
// FieldDef is always set by the store; an assignment without one is skipped
// rather than guessed at.
//
// It also refuses, for every offered active field, a value over the field's
// length limit, required or not: creation otherwise drops such a value with a
// warning after the ticket exists, and the guest and REST doors are meant to say
// 400 before anything is written. guest says whose words the refusal uses.
func checkRequired(offered []Assignment, raw map[string]string, guest bool) error {
	values, dup := valuesByID(raw)
	var missing []string
	for _, a := range offered {
		def := a.FieldDef
		if def == nil || !def.Active {
			continue
		}
		if v, ok := values[a.FieldDefID]; ok && tooLong(*def, v) {
			return refusalFor(*def, v, ReasonTooLong, guest)
		}
		if !a.VisibleOnNew || !a.RequiredOnNew {
			continue
		}
		if dup[a.FieldDefID] {
			return Invalid(fmt.Sprintf("field %q is listed twice", def.Name))
		}
		v := values[a.FieldDefID]
		if strings.TrimSpace(v) == "" {
			missing = append(missing, fmt.Sprintf("%q", def.Name))
			continue
		}
		if _, reason := checkValue(*def, v); reason != "" {
			return refusalFor(*def, v, reason, guest)
		}
	}
	switch len(missing) {
	case 0:
		return nil
	case 1:
		return Invalid(fmt.Sprintf("custom field %s is required", missing[0]))
	}
	return Invalid("custom fields are required: " + strings.Join(missing, ", "))
}

// CheckRequired refuses, as a 400, a ticket about to be created without a value
// for a field its classification requires. It is the server's rule, not the
// form's: before 1.3.0 only the new-ticket form held anyone to it. Called before
// the ticket is created so that no tracking number is spent on a refusal.
//
// The check and the create are not one transaction: an administrator who makes a
// field required between them lets one ticket through without it. Accepted; the
// ticket is not wrong, only the form was a moment out of date.
func (s *Service) CheckRequired(ctx context.Context, categoryID uuid.UUID, typeID, itemID *uuid.UUID, raw map[string]string) error {
	_, err := s.CheckCreation(ctx, categoryID, typeID, itemID, raw)
	return err
}

// CheckCreation is CheckRequired that also says whether the classification has a
// required field. The creation doors need that if the write of the values fails
// afterwards: a ticket that was to hold a required value and does not is what
// the rule exists to prevent, so for such a ticket the failure fails the request,
// while for any other the values stay best-effort as they always were. It is
// answered here, from the lookup the check already made, and not asked after the
// failure, when the database may be the thing that failed.
func (s *Service) CheckCreation(ctx context.Context, categoryID uuid.UUID, typeID, itemID *uuid.UUID, raw map[string]string) (hasRequired bool, err error) {
	fields, err := s.ResolveFieldsForCTI(ctx, categoryID, typeID, itemID)
	if err != nil {
		return false, fmt.Errorf("resolving the ticket's fields: %w", err)
	}
	for _, a := range fields {
		if a.FieldDef != nil && a.FieldDef.Active && a.VisibleOnNew && a.RequiredOnNew {
			hasRequired = true
		}
	}
	return hasRequired, checkRequired(fields, raw, false)
}

// GuestFields is the fields an anonymous visitor filing under the category is
// offered: the category's own attachments that an administrator switched on for
// guests, that are shown on the new-ticket form, and whose field is active. A
// guest picks a category and nothing below it, so nothing attached to a type or
// an item can be here. Everything else a guest asks about is judged against this
// list, which is why it is built from an explicit opt-in and never from "not
// marked internal": a field added later is not offered until someone says so.
func (s *Service) GuestFields(ctx context.Context, categoryID uuid.UUID) ([]Assignment, error) {
	all, err := s.store.ListAssignments(ctx, CategoryScope, categoryID)
	if err != nil {
		return nil, fmt.Errorf("listing the category's fields: %w", err)
	}
	var out []Assignment
	for _, a := range all {
		if a.VisibleToGuests && a.VisibleOnNew && a.FieldDef != nil && a.FieldDef.Active {
			out = append(out, a)
		}
	}
	return out, nil
}

// CheckGuestValues is what the guest endpoint asks before it files anything. It
// is strict where CheckRequired is not: a guest is told no rather than warned,
// because the form is ours and an anonymous caller has nothing to gain from a
// partial save. Every key must be a field offered to guests (naming any other,
// an empty value included, is refused, and the message does not say whether such
// a field exists); every value must be one the field accepts; every required
// offered field needs one. Required is judged only over the offered fields, so
// a field the staff form requires but guests are not offered does not stop a
// guest filing.
func (s *Service) CheckGuestValues(ctx context.Context, categoryID uuid.UUID, raw map[string]string) error {
	offered, err := s.GuestFields(ctx, categoryID)
	if err != nil {
		return err
	}
	defs := make(map[uuid.UUID]*FieldDef, len(offered))
	for _, a := range offered {
		defs[a.FieldDefID] = a.FieldDef
	}
	seen := make(map[uuid.UUID]bool, len(raw))
	for key, v := range raw {
		id, err := uuid.Parse(key)
		def, ok := defs[id]
		if err != nil || !ok {
			return Invalid("custom_fields names a field that is not offered to guests")
		}
		if seen[id] {
			return Invalid(fmt.Sprintf("field %q is listed twice", def.Name))
		}
		seen[id] = true
		if v == "" {
			continue
		}
		if _, reason := checkValue(*def, v); reason != "" {
			return refusalFor(*def, v, reason, true)
		}
	}
	return checkRequired(offered, raw, true)
}
