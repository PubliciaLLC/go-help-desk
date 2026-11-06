package customfield_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// fakeStore is an in-memory customfield.Store. DeleteAssignment follows the
// contract the real store now has (#418): deleting an id that names nothing
// returns an error wrapping ErrNotFound.
type fakeStore struct {
	defs        map[uuid.UUID]customfield.FieldDef
	assignments map[uuid.UUID]customfield.Assignment
	values      map[[2]uuid.UUID]string
	audit       []audit.Entry // what the store was asked to write with each change
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		defs:        map[uuid.UUID]customfield.FieldDef{},
		assignments: map[uuid.UUID]customfield.Assignment{},
		values:      map[[2]uuid.UUID]string{},
	}
}

func (f *fakeStore) CreateFieldDef(_ context.Context, d customfield.FieldDef, entries ...audit.Entry) (customfield.FieldDef, error) {
	f.defs[d.ID] = d
	f.audit = append(f.audit, entries...)
	return d, nil
}
func (f *fakeStore) ListFieldDefs(context.Context) ([]customfield.FieldDef, error) { return nil, nil }
func (f *fakeStore) GetFieldDef(_ context.Context, id uuid.UUID) (customfield.FieldDef, error) {
	d, ok := f.defs[id]
	if !ok {
		return customfield.FieldDef{}, fmt.Errorf("field %s: %w", id, customfield.ErrNotFound)
	}
	return d, nil
}

// fakeHeld answers from the fake's values, as the real store answers from the
// table inside the transaction.
type fakeHeld struct {
	f  *fakeStore
	id uuid.UUID
}

func (h fakeHeld) Any() (bool, error) {
	for k := range h.f.values {
		if k[1] == h.id {
			return true, nil
		}
	}
	return false, nil
}
func (h fakeHeld) OneOf(values []string) (string, bool, error) {
	for k, v := range h.f.values {
		if k[1] == h.id && slices.Contains(values, v) {
			return v, true, nil
		}
	}
	return "", false, nil
}

func (f *fakeStore) UpdateFieldDef(_ context.Context, id uuid.UUID, decide func(customfield.FieldDef, customfield.Held) (customfield.FieldDef, []audit.Entry, error)) (customfield.FieldDef, error) {
	cur, ok := f.defs[id]
	if !ok {
		return customfield.FieldDef{}, fmt.Errorf("field %s: %w", id, customfield.ErrNotFound)
	}
	next, entries, err := decide(cur, fakeHeld{f, id})
	if err != nil {
		return customfield.FieldDef{}, err
	}
	f.defs[id] = next
	f.audit = append(f.audit, entries...)
	return next, nil
}
func (f *fakeStore) ReorderFieldDefs(_ context.Context, decide func([]customfield.FieldDef) ([]uuid.UUID, []audit.Entry, error)) error {
	cur := make([]customfield.FieldDef, 0, len(f.defs))
	for _, d := range f.defs {
		cur = append(cur, d)
	}
	ids, entries, err := decide(cur)
	if err != nil {
		return err
	}
	for i, id := range ids {
		d := f.defs[id]
		d.SortOrder = i + 1
		f.defs[id] = d
	}
	f.audit = append(f.audit, entries...)
	return nil
}
func (f *fakeStore) CountValues(_ context.Context, id uuid.UUID) (int, error) {
	n := 0
	for k := range f.values {
		if k[1] == id {
			n++
		}
	}
	return n, nil
}
func (f *fakeStore) CountValuesByValue(_ context.Context, id uuid.UUID) (map[string]int, error) {
	out := map[string]int{}
	for k, v := range f.values {
		if k[1] == id {
			out[v]++
		}
	}
	return out, nil
}
func (f *fakeStore) info(a customfield.Assignment) customfield.AssignmentInfo {
	return customfield.AssignmentInfo{FieldName: f.defs[a.FieldDefID].Name, ScopePath: "path"}
}
func (f *fakeStore) CreateAssignment(_ context.Context, a customfield.Assignment, entry ...func(customfield.AssignmentInfo) audit.Entry) (customfield.Assignment, error) {
	f.assignments[a.ID] = a
	for _, build := range entry {
		f.audit = append(f.audit, build(f.info(a)))
	}
	return a, nil
}
func (f *fakeStore) ListAssignments(_ context.Context, st customfield.ScopeType, id uuid.UUID) ([]customfield.Assignment, error) {
	var out []customfield.Assignment
	for _, a := range f.assignments {
		if a.ScopeType == st && a.ScopeID == id {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *fakeStore) ListPlacements(context.Context, uuid.UUID) ([]customfield.Placement, error) {
	return nil, nil
}
func (f *fakeStore) GetAssignment(_ context.Context, id uuid.UUID) (customfield.Assignment, error) {
	return f.assignments[id], nil
}
func (f *fakeStore) UpdateAssignment(_ context.Context, a customfield.Assignment, entries func(customfield.Assignment, customfield.AssignmentInfo) []audit.Entry) error {
	f.audit = append(f.audit, entries(f.assignments[a.ID], f.info(a))...)
	f.assignments[a.ID] = a
	return nil
}
func (f *fakeStore) DeleteAssignment(_ context.Context, id uuid.UUID, entry func(customfield.Assignment, customfield.AssignmentInfo) audit.Entry) error {
	a, ok := f.assignments[id]
	if !ok {
		return fmt.Errorf("field assignment %s: %w", id, customfield.ErrNotFound)
	}
	f.audit = append(f.audit, entry(a, f.info(a)))
	delete(f.assignments, id)
	return nil
}
func (f *fakeStore) UpsertValue(_ context.Context, t, d uuid.UUID, v string) error {
	f.values[[2]uuid.UUID{t, d}] = v
	return nil
}
func (f *fakeStore) DeleteValue(_ context.Context, t, d uuid.UUID) error {
	delete(f.values, [2]uuid.UUID{t, d})
	return nil
}
func (f *fakeStore) ListValuesForTicket(context.Context, uuid.UUID) ([]customfield.TicketFieldValue, error) {
	return nil, nil
}

// SetValues follows the contract the real store has: decide sees the
// definitions of the entries that name one, and what it returns is what is
// written, nothing if it refuses.
func (f *fakeStore) SetValues(_ context.Context, ticketID uuid.UUID, entries map[uuid.UUID]string, decide func(map[uuid.UUID]customfield.FieldDef) (map[uuid.UUID]string, error)) error {
	defs := map[uuid.UUID]customfield.FieldDef{}
	for id := range entries {
		if d, ok := f.defs[id]; ok {
			defs[id] = d
		}
	}
	write, err := decide(defs)
	if err != nil {
		return err
	}
	for id, v := range write {
		if v == "" {
			delete(f.values, [2]uuid.UUID{ticketID, id})
		} else {
			f.values[[2]uuid.UUID{ticketID, id}] = v
		}
	}
	return nil
}

func TestCreateFieldDef_Validation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		in      string
		ft      customfield.FieldType
		options []string
		wantErr bool
	}{
		{"valid text", "Serial", customfield.FieldTypeText, nil, false},
		{"blank name", "   ", customfield.FieldTypeText, nil, true},
		{"unknown type", "X", customfield.FieldType("bogus"), nil, true},
		{"select without options", "Pick", customfield.FieldTypeSelect, nil, true},
		{"select with options", "Pick", customfield.FieldTypeSelect, []string{"a"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def, err := customfield.NewService(newFakeStore()).CreateFieldDef(ctx, tc.in, tc.ft, tc.options, 1)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.True(t, def.Active, "a new field starts active")
		})
	}
}

func TestCreateFieldDef_TrimsName(t *testing.T) {
	def, err := customfield.NewService(newFakeStore()).CreateFieldDef(context.Background(), "  Serial  ", customfield.FieldTypeText, nil, 1)
	require.NoError(t, err)
	require.Equal(t, "Serial", def.Name)
}

func TestSetValue(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	ticketID := uuid.New()

	sel, err := svc.CreateFieldDef(ctx, "Colour", customfield.FieldTypeSelect, []string{"red", "blue"}, 1)
	require.NoError(t, err)
	key := [2]uuid.UUID{ticketID, sel.ID}

	require.NoError(t, svc.SetValue(ctx, ticketID, sel.ID, "red"))
	require.Equal(t, "red", store.values[key])

	require.Error(t, svc.SetValue(ctx, ticketID, sel.ID, "green"), "a select value outside the options is refused")
	require.Equal(t, "red", store.values[key], "a refused value leaves the stored one alone")

	require.NoError(t, svc.SetValue(ctx, ticketID, sel.ID, ""), "an empty value deletes the row")
	require.NotContains(t, store.values, key)

	require.ErrorIs(t, svc.SetValue(ctx, ticketID, uuid.New(), "x"), customfield.ErrNotFound,
		"an unknown field keeps its not-found cause so the HTTP layer can say 404")

	sel.Active = false
	require.NoError(t, svc.UpdateFieldDef(ctx, sel))
	require.Error(t, svc.SetValue(ctx, ticketID, sel.ID, "blue"), "a deactivated field takes no new value")

	// A clear is a write too. It used to reach DeleteValue before the active
	// check and wipe the stored value.
	store.values[key] = "red"
	require.Error(t, svc.SetValue(ctx, ticketID, sel.ID, ""), "a deactivated field cannot be cleared")
	require.Equal(t, "red", store.values[key], "a refused clear leaves the stored value alone")
}

func TestSetValues(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	ticketID := uuid.New()

	text, err := svc.CreateFieldDef(ctx, "Serial", customfield.FieldTypeText, nil, 1)
	require.NoError(t, err)
	sel, err := svc.CreateFieldDef(ctx, "Colour", customfield.FieldTypeSelect, []string{"red", "blue"}, 2)
	require.NoError(t, err)
	old, err := svc.CreateFieldDef(ctx, "Old", customfield.FieldTypeText, nil, 3)
	require.NoError(t, err)
	key := func(id uuid.UUID) [2]uuid.UUID { return [2]uuid.UUID{ticketID, id} }
	store.values[key(old.ID)] = "stale"

	t.Run("every entry is written, a blank one clears", func(t *testing.T) {
		require.NoError(t, svc.SetValues(ctx, ticketID, map[uuid.UUID]string{
			text.ID: "SN-1", sel.ID: "red", old.ID: "",
		}))
		require.Equal(t, "SN-1", store.values[key(text.ID)])
		require.Equal(t, "red", store.values[key(sel.ID)])
		require.NotContains(t, store.values, key(old.ID))
	})

	// One bad entry among good ones: none of them lands. Each case starts from
	// the same stored state so "nothing written" is a comparison, not a hope.
	inactive, err := svc.CreateFieldDef(ctx, "Retired", customfield.FieldTypeText, nil, 4)
	require.NoError(t, err)
	inactive.Active = false
	require.NoError(t, svc.UpdateFieldDef(ctx, inactive))

	for _, tc := range []struct {
		name    string
		entries map[uuid.UUID]string
		wantErr error
	}{
		{"option outside the list", map[uuid.UUID]string{text.ID: "SN-2", sel.ID: "green"}, customfield.ErrValidation},
		{"deactivated field", map[uuid.UUID]string{text.ID: "SN-2", inactive.ID: "x"}, customfield.ErrValidation},
		{"clear of a deactivated field", map[uuid.UUID]string{text.ID: "SN-2", inactive.ID: ""}, customfield.ErrValidation},
		{"unknown field", map[uuid.UUID]string{text.ID: "SN-2", uuid.New(): "x"}, customfield.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := map[[2]uuid.UUID]string{}
			for k, v := range store.values {
				before[k] = v
			}
			require.ErrorIs(t, svc.SetValues(ctx, ticketID, tc.entries), tc.wantErr)
			require.Equal(t, before, store.values, "a refused entry leaves every other entry unwritten")
		})
	}

	t.Run("nothing to write is not an error", func(t *testing.T) {
		require.NoError(t, svc.SetValues(ctx, ticketID, nil))
	})
}

// Category fields come first, then type, then item: the order the new-ticket
// form shows them in.
func TestResolveFieldsForCTI_OrdersByScopeLevel(t *testing.T) {
	ctx := context.Background()
	svc := customfield.NewService(newFakeStore())
	cat, typ, item := uuid.New(), uuid.New(), uuid.New()
	// A different field at each level: one field at all three is shown once
	// (bug 4), which is not what this test is about.
	itemDef, catDef, typeDef := uuid.New(), uuid.New(), uuid.New()

	ia, err := svc.CreateAssignment(ctx, itemDef, customfield.ItemScope, item, 1, true, false)
	require.NoError(t, err)
	ca, err := svc.CreateAssignment(ctx, catDef, customfield.CategoryScope, cat, 1, true, false)
	require.NoError(t, err)
	ta, err := svc.CreateAssignment(ctx, typeDef, customfield.TypeScope, typ, 1, true, false)
	require.NoError(t, err)

	got, err := svc.ResolveFieldsForCTI(ctx, cat, &typ, &item)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{ca.ID, ta.ID, ia.ID}, []uuid.UUID{got[0].ID, got[1].ID, got[2].ID})

	got, err = svc.ResolveFieldsForCTI(ctx, cat, nil, nil)
	require.NoError(t, err)
	require.Len(t, got, 1, "no type or item means only the category's fields")
}

// Bug 4 (#603): a field attached at more than one of the ticket's levels
// reaches the ticket once, not once per attachment.
func TestResolveFields_FieldOnCategoryAndTypeAppearsOnce(t *testing.T) {
	ctx := context.Background()
	svc := customfield.NewService(newFakeStore())
	cat, typ := uuid.New(), uuid.New()
	def, other := uuid.New(), uuid.New()

	_, err := svc.CreateAssignment(ctx, def, customfield.CategoryScope, cat, 1, true, false)
	require.NoError(t, err)
	_, err = svc.CreateAssignment(ctx, def, customfield.TypeScope, typ, 1, true, false)
	require.NoError(t, err)
	_, err = svc.CreateAssignment(ctx, other, customfield.CategoryScope, cat, 2, true, false)
	require.NoError(t, err)

	got, err := svc.ResolveFieldsForCTI(ctx, cat, &typ, nil)
	require.NoError(t, err)
	require.Len(t, got, 2, "one entry for the overlapped field, one for the other")

	seen := map[uuid.UUID]int{}
	for _, a := range got {
		seen[a.FieldDefID]++
	}
	require.Equal(t, 1, seen[def], "the overlapped field appears once")
	require.Equal(t, 1, seen[other], "a field attached once is untouched")
}

// The most specific attachment wins, for its settings and for its level.
// The two attachments carry different settings so a loser leaking through
// shows.
func TestResolveFields_MostSpecificAttachmentWins(t *testing.T) {
	cat, typ, item := uuid.New(), uuid.New(), uuid.New()
	scopeID := map[customfield.ScopeType]uuid.UUID{
		customfield.CategoryScope: cat,
		customfield.TypeScope:     typ,
		customfield.ItemScope:     item,
	}

	for _, tc := range []struct {
		name                       string
		general, specific          customfield.ScopeType
		generalVisible, generalReq bool
		specificVisible, specReq   bool
	}{
		{"category and type", customfield.CategoryScope, customfield.TypeScope, true, true, true, false},
		{"type and item", customfield.TypeScope, customfield.ItemScope, true, false, true, true},
		{"category and item", customfield.CategoryScope, customfield.ItemScope, false, false, true, true},
		{"the specific one hides what the general one shows", customfield.CategoryScope, customfield.ItemScope, true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc := customfield.NewService(newFakeStore())
			def := uuid.New()

			_, err := svc.CreateAssignment(ctx, def, tc.general, scopeID[tc.general], 1, tc.generalVisible, tc.generalReq)
			require.NoError(t, err)
			want, err := svc.CreateAssignment(ctx, def, tc.specific, scopeID[tc.specific], 1, tc.specificVisible, tc.specReq)
			require.NoError(t, err)

			got, err := svc.ResolveFieldsForCTI(ctx, cat, &typ, &item)
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Equal(t, want.ID, got[0].ID, "the more specific attachment is the one returned")
			require.Equal(t, tc.specific, got[0].ScopeType, "level")
			require.Equal(t, tc.specificVisible, got[0].VisibleOnNew, "visible_on_new")
			require.Equal(t, tc.specReq, got[0].RequiredOnNew, "required_on_new")
		})
	}
}

// An overlapped field is listed with its winning level's fields, so the
// grouping the form shows agrees with the level reported.
func TestResolveFields_OverlappedFieldSitsAtItsWinningLevel(t *testing.T) {
	ctx := context.Background()
	svc := customfield.NewService(newFakeStore())
	cat, typ := uuid.New(), uuid.New()
	moved, stays := uuid.New(), uuid.New()

	_, err := svc.CreateAssignment(ctx, moved, customfield.CategoryScope, cat, 1, true, false)
	require.NoError(t, err)
	_, err = svc.CreateAssignment(ctx, stays, customfield.CategoryScope, cat, 2, true, false)
	require.NoError(t, err)
	_, err = svc.CreateAssignment(ctx, moved, customfield.TypeScope, typ, 1, true, false)
	require.NoError(t, err)

	got, err := svc.ResolveFieldsForCTI(ctx, cat, &typ, nil)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, stays, got[0].FieldDefID)
	require.Equal(t, customfield.CategoryScope, got[0].ScopeType)
	require.Equal(t, moved, got[1].FieldDefID)
	require.Equal(t, customfield.TypeScope, got[1].ScopeType)
}

// Deleting an assignment that does not exist reports ErrNotFound (404 at the
// HTTP layer) instead of succeeding quietly (#418), and the service passes the
// sentinel through unmasked.
func TestDeleteAssignment_UnknownIDIsErrNotFound(t *testing.T) {
	ctx := context.Background()
	svc := customfield.NewService(newFakeStore())
	a, err := svc.CreateAssignment(ctx, uuid.New(), customfield.CategoryScope, uuid.New(), 1, true, false)
	require.NoError(t, err)

	require.NoError(t, svc.DeleteAssignment(ctx, a.ID))
	require.ErrorIs(t, svc.DeleteAssignment(ctx, a.ID), customfield.ErrNotFound, "second delete")
	require.ErrorIs(t, svc.DeleteAssignment(ctx, uuid.New()), customfield.ErrNotFound, "never existed")
}

// ── Reorder ──────────────────────────────────────────────────────────────────

func TestReorderFieldDefs(t *testing.T) {
	ctx := context.Background()
	mk := func() (*customfield.Service, *fakeStore, []customfield.FieldDef) {
		store := newFakeStore()
		svc := customfield.NewService(store)
		var defs []customfield.FieldDef
		for i, n := range []string{"A", "B", "C"} {
			d, err := svc.CreateFieldDef(ctx, n, customfield.FieldTypeText, nil, i+1)
			require.NoError(t, err)
			defs = append(defs, d)
		}
		store.audit = nil
		return svc, store, defs
	}

	t.Run("position n becomes number n", func(t *testing.T) {
		svc, store, d := mk()
		require.NoError(t, svc.ReorderFieldDefs(ctx, []uuid.UUID{d[2].ID, d[0].ID, d[1].ID}))
		require.Equal(t, 1, store.defs[d[2].ID].SortOrder)
		require.Equal(t, 2, store.defs[d[0].ID].SortOrder)
		require.Equal(t, 3, store.defs[d[1].ID].SortOrder)
		require.Len(t, store.audit, 3, "all three numbers changed")
	})
	t.Run("a repeated id is the caller's mistake", func(t *testing.T) {
		svc, store, d := mk()
		err := svc.ReorderFieldDefs(ctx, []uuid.UUID{d[0].ID, d[0].ID, d[1].ID})
		require.ErrorIs(t, err, customfield.ErrValidation)
		require.Equal(t, 1, store.defs[d[0].ID].SortOrder)
	})
	t.Run("a list that does not match the stored fields is stale", func(t *testing.T) {
		svc, store, d := mk()
		for name, ids := range map[string][]uuid.UUID{
			"one missing":   {d[0].ID, d[1].ID},
			"one unknown":   {d[0].ID, d[1].ID, uuid.New()},
			"one too many":  {d[0].ID, d[1].ID, d[2].ID, uuid.New()},
			"empty vs some": {},
		} {
			require.ErrorIs(t, svc.ReorderFieldDefs(ctx, ids), customfield.ErrConflict, name)
		}
		require.Equal(t, 1, store.defs[d[0].ID].SortOrder, "nothing was written")
		require.Empty(t, store.audit)
	})
	t.Run("no fields and an empty list", func(t *testing.T) {
		require.NoError(t, customfield.NewService(newFakeStore()).ReorderFieldDefs(ctx, []uuid.UUID{}))
	})
}

// ── Options: retired, delete, type lock ──────────────────────────────────────

func TestPatchFieldDef_OptionRules(t *testing.T) {
	ctx := context.Background()
	strs := func(s ...string) []string { return append([]string{}, s...) }
	typ := func(t customfield.FieldType) *customfield.FieldType { return &t }

	for _, tc := range []struct {
		name    string
		options []string // stored before
		retired []string
		held    []string // values a ticket holds
		patch   customfield.FieldDefPatch
		wantErr error
	}{
		{"an option in both lists is refused", strs("a", "b"), nil, nil,
			customfield.FieldDefPatch{Options: strs("a", "b"), RetiredOptions: strs("b")}, customfield.ErrValidation},
		{"a value cannot enter retired without being an option", strs("a"), nil, nil,
			customfield.FieldDefPatch{RetiredOptions: strs("zzz")}, customfield.ErrValidation},
		{"a select field needs an option in use", strs("a"), nil, nil,
			customfield.FieldDefPatch{Options: strs(), RetiredOptions: strs("a")}, customfield.ErrValidation},
		{"retiring an option is a move", strs("a", "b"), nil, strs("b"),
			customfield.FieldDefPatch{Options: strs("a"), RetiredOptions: strs("b")}, nil},
		{"bringing a retired option back", strs("a"), strs("b"), strs("b"),
			customfield.FieldDefPatch{Options: strs("a", "b"), RetiredOptions: strs()}, nil},
		{"deleting an unused option", strs("a", "b"), nil, nil,
			customfield.FieldDefPatch{Options: strs("a")}, nil},
		{"deleting an option a ticket holds", strs("a", "b"), nil, strs("b"),
			customfield.FieldDefPatch{Options: strs("a")}, customfield.ErrConflict},
		{"deleting a retired option a ticket holds", strs("a"), strs("b"), strs("b"),
			customfield.FieldDefPatch{RetiredOptions: strs()}, customfield.ErrConflict},
		{"deleting a retired option nobody holds", strs("a"), strs("b"), nil,
			customfield.FieldDefPatch{RetiredOptions: strs()}, nil},
		{"leaving options alone keeps them", strs("a", "b"), nil, strs("a", "b"),
			customfield.FieldDefPatch{SortOrder: new(int)}, nil},
		{"the type changes while unused", strs("a"), nil, nil,
			customfield.FieldDefPatch{FieldType: typ(customfield.FieldTypeText)}, nil},
		{"the type is locked once a ticket holds a value", strs("a"), nil, strs("a"),
			customfield.FieldDefPatch{FieldType: typ(customfield.FieldTypeText)}, customfield.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			svc := customfield.NewService(store)
			def, err := svc.CreateFieldDef(ctx, "Pick", customfield.FieldTypeSelect, tc.options, 1)
			require.NoError(t, err)
			def.RetiredOptions = tc.retired
			store.defs[def.ID] = def
			for _, v := range tc.held {
				store.values[[2]uuid.UUID{uuid.New(), def.ID}] = v
			}
			store.audit = nil

			got, err := svc.PatchFieldDef(ctx, def.ID, tc.patch)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, def, store.defs[def.ID], "a refused change writes nothing")
				require.Empty(t, store.audit, "and no entry")
				return
			}
			require.NoError(t, err)
			require.Equal(t, got, store.defs[def.ID])
		})
	}
}

func TestPatchFieldDef_TypeChangeAfterTheValueIsCleared(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	def, err := svc.CreateFieldDef(ctx, "Cost", customfield.FieldTypeText, nil, 1)
	require.NoError(t, err)
	ticket := uuid.New()
	number := customfield.FieldTypeNumber

	require.NoError(t, svc.SetValue(ctx, ticket, def.ID, "abc"))
	_, err = svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{FieldType: &number})
	require.ErrorIs(t, err, customfield.ErrConflict)

	require.NoError(t, svc.SetValue(ctx, ticket, def.ID, ""))
	_, err = svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{FieldType: &number})
	require.NoError(t, err, "unlocked again once no ticket holds a value")
}

func TestPatchFieldDef_AuditsWithTheActor(t *testing.T) {
	store := newFakeStore()
	svc := customfield.NewService(store)
	actor := uuid.New()
	ctx := customfield.WithActor(context.Background(), actor)

	def, err := svc.CreateFieldDef(ctx, "Dept", customfield.FieldTypeText, nil, 1)
	require.NoError(t, err)
	name := "Department"
	_, err = svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{Name: &name})
	require.NoError(t, err)

	require.Len(t, store.audit, 2)
	require.Equal(t, "custom_field_created", store.audit[0].Action)
	require.Equal(t, "custom_field_renamed", store.audit[1].Action)
	for _, e := range store.audit {
		require.NotNil(t, e.ActorID)
		require.Equal(t, actor, *e.ActorID)
		require.Equal(t, def.ID, e.EntityID)
	}

	// No actor in the context: the system made the change.
	order := 7
	_, err = svc.PatchFieldDef(context.Background(), def.ID, customfield.FieldDefPatch{SortOrder: &order})
	require.NoError(t, err)
	require.Nil(t, store.audit[len(store.audit)-1].ActorID)
}

// ── Values: retired options and numbers ──────────────────────────────────────

func TestSetValue_RefusesARetiredOption(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	def, err := svc.CreateFieldDef(ctx, "Pick", customfield.FieldTypeSelect, []string{"a", "b"}, 1)
	require.NoError(t, err)
	def.Options, def.RetiredOptions = []string{"a"}, []string{"b"}
	store.defs[def.ID] = def
	ticket := uuid.New()

	require.ErrorIs(t, svc.SetValue(ctx, ticket, def.ID, "b"), customfield.ErrValidation)
	require.ErrorIs(t, svc.SetValues(ctx, ticket, map[uuid.UUID]string{def.ID: "b"}), customfield.ErrValidation)
	require.NoError(t, svc.SetValues(ctx, ticket, map[uuid.UUID]string{def.ID: "a"}))

	// A ticket that holds the retired value can clear it.
	store.values[[2]uuid.UUID{ticket, def.ID}] = "b"
	require.NoError(t, svc.SetValues(ctx, ticket, map[uuid.UUID]string{def.ID: ""}))
}

func TestSetValues_NumberFields(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	num, err := svc.CreateFieldDef(ctx, "Cost", customfield.FieldTypeNumber, nil, 1)
	require.NoError(t, err)
	other, err := svc.CreateFieldDef(ctx, "Serial", customfield.FieldTypeText, nil, 2)
	require.NoError(t, err)
	ticket := uuid.New()
	key := [2]uuid.UUID{ticket, num.ID}

	t.Run("stored as written, trimmed", func(t *testing.T) {
		require.NoError(t, svc.SetValues(ctx, ticket, map[uuid.UUID]string{num.ID: " 1.50 "}))
		require.Equal(t, "1.50", store.values[key])
	})
	t.Run("text is refused and nothing else is written", func(t *testing.T) {
		err := svc.SetValues(ctx, ticket, map[uuid.UUID]string{num.ID: "abc", other.ID: "x"})
		require.ErrorIs(t, err, customfield.ErrValidation)
		require.EqualError(t, err, `value "abc" is not a number for field "Cost"`)
		require.NotContains(t, store.values, [2]uuid.UUID{ticket, other.ID})
		require.Equal(t, "1.50", store.values[key])
	})
	t.Run("spaces only is a clear", func(t *testing.T) {
		require.NoError(t, svc.SetValues(ctx, ticket, map[uuid.UUID]string{num.ID: "   "}))
		require.NotContains(t, store.values, key)
	})
}

// ── Usage ────────────────────────────────────────────────────────────────────

func TestUsage(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	sel, err := svc.CreateFieldDef(ctx, "Brand", customfield.FieldTypeSelect, []string{"Dell", "HP"}, 1)
	require.NoError(t, err)
	sel.RetiredOptions = []string{"Acer"}
	store.defs[sel.ID] = sel
	for _, v := range []string{"Dell", "Dell", "Acer", "Zebra", "Apple"} {
		store.values[[2]uuid.UUID{uuid.New(), sel.ID}] = v
	}
	text, err := svc.CreateFieldDef(ctx, "Serial", customfield.FieldTypeText, nil, 2)
	require.NoError(t, err)
	store.values[[2]uuid.UUID{uuid.New(), text.ID}] = "SN"

	t.Run("select: in use, then retired, then in neither list; zero for unheld", func(t *testing.T) {
		u, err := svc.Usage(ctx, sel.ID)
		require.NoError(t, err)
		require.Equal(t, customfield.Usage{TicketsWithValue: 5, Values: []customfield.ValueCount{
			{Value: "Dell", Tickets: 2}, {Value: "HP", Tickets: 0},
			{Value: "Acer", Tickets: 1},
			{Value: "Apple", Tickets: 1}, {Value: "Zebra", Tickets: 1},
		}}, u)
	})
	t.Run("other types have no values, and the slice is not nil", func(t *testing.T) {
		u, err := svc.Usage(ctx, text.ID)
		require.NoError(t, err)
		require.Equal(t, 1, u.TicketsWithValue)
		require.NotNil(t, u.Values)
		require.Empty(t, u.Values)
	})
	t.Run("unknown field", func(t *testing.T) {
		_, err := svc.Usage(ctx, uuid.New())
		require.ErrorIs(t, err, customfield.ErrNotFound)
	})
}

// ── Creation: warnings ───────────────────────────────────────────────────────

func TestSetCreationValues_WarningsNameEachDroppedField(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	cat := uuid.New()
	mk := func(name string, ft customfield.FieldType, opts []string) customfield.FieldDef {
		d, err := svc.CreateFieldDef(ctx, name, ft, opts, 1)
		require.NoError(t, err)
		_, err = svc.CreateAssignment(ctx, d.ID, customfield.CategoryScope, cat, 1, true, false)
		require.NoError(t, err)
		return d
	}
	good := mk("Serial", customfield.FieldTypeText, nil)
	inactive := mk("Gone", customfield.FieldTypeText, nil)
	inactive.Active = false
	store.defs[inactive.ID] = inactive
	pick := mk("Pick", customfield.FieldTypeSelect, []string{"a", "b"})
	pick.Options, pick.RetiredOptions = []string{"a"}, []string{"b"}
	store.defs[pick.ID] = pick
	num := mk("Cost", customfield.FieldTypeNumber, nil)
	elsewhere, err := svc.CreateFieldDef(ctx, "Elsewhere", customfield.FieldTypeText, nil, 9)
	require.NoError(t, err)
	ticket := uuid.New()
	unknown := uuid.New()

	raw := map[string]string{
		good.ID.String():      "SN-1",
		"not-a-uuid":          "x",
		elsewhere.ID.String(): "x",
		unknown.String():      "x",
		inactive.ID.String():  "x",
		pick.ID.String():      "b",
		num.ID.String():       "abc",
	}
	warnings, err := svc.SetCreationValues(ctx, ticket, cat, nil, nil, raw)
	require.NoError(t, err)

	want := []customfield.Warning{
		{FieldID: elsewhere.ID.String(), Reason: customfield.ReasonNotOnTicket},
		{FieldID: inactive.ID.String(), Reason: customfield.ReasonInactive},
		{FieldID: "not-a-uuid", Reason: customfield.ReasonInvalidFieldID},
		{FieldID: num.ID.String(), Reason: customfield.ReasonNotANumber},
		{FieldID: pick.ID.String(), Reason: customfield.ReasonRetiredOption},
		{FieldID: unknown.String(), Reason: customfield.ReasonNotOnTicket},
	}
	slices.SortFunc(want, func(a, b customfield.Warning) int { return strings.Compare(a.FieldID, b.FieldID) })
	require.Equal(t, want, warnings, "sorted by field_id, one per dropped entry")
	require.Equal(t, "SN-1", store.values[[2]uuid.UUID{ticket, good.ID}], "the valid entry was written")
	require.Len(t, store.values, 1)

	t.Run("a value outside the options is not_an_option", func(t *testing.T) {
		w, err := svc.SetCreationValues(ctx, uuid.New(), cat, nil, nil, map[string]string{pick.ID.String(): "zzz"})
		require.NoError(t, err)
		require.Equal(t, []customfield.Warning{{FieldID: pick.ID.String(), Reason: customfield.ReasonNotAnOption}}, w)
	})
	t.Run("an empty value is not a warning and writes nothing", func(t *testing.T) {
		tk := uuid.New()
		w, err := svc.SetCreationValues(ctx, tk, cat, nil, nil, map[string]string{good.ID.String(): "", "not-a-uuid": ""})
		require.NoError(t, err)
		require.Nil(t, w)
		require.NotContains(t, store.values, [2]uuid.UUID{tk, good.ID})
	})
	t.Run("nothing dropped means no warnings at all", func(t *testing.T) {
		w, err := svc.SetCreationValues(ctx, uuid.New(), cat, nil, nil, map[string]string{good.ID.String(): "ok"})
		require.NoError(t, err)
		require.Nil(t, w)
	})
}
