package customfield_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// fakeStore is an in-memory customfield.Store. DeleteAssignment follows the
// contract the real store now has (#418): deleting an id that names nothing
// returns an error wrapping ErrNotFound.
type fakeStore struct {
	defs        map[uuid.UUID]customfield.FieldDef
	assignments map[uuid.UUID]customfield.Assignment
	values      map[[2]uuid.UUID]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		defs:        map[uuid.UUID]customfield.FieldDef{},
		assignments: map[uuid.UUID]customfield.Assignment{},
		values:      map[[2]uuid.UUID]string{},
	}
}

func (f *fakeStore) CreateFieldDef(_ context.Context, d customfield.FieldDef) (customfield.FieldDef, error) {
	f.defs[d.ID] = d
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
func (f *fakeStore) UpdateFieldDef(_ context.Context, d customfield.FieldDef) error {
	f.defs[d.ID] = d
	return nil
}
func (f *fakeStore) CreateAssignment(_ context.Context, a customfield.Assignment) (customfield.Assignment, error) {
	f.assignments[a.ID] = a
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
func (f *fakeStore) GetAssignment(_ context.Context, id uuid.UUID) (customfield.Assignment, error) {
	return f.assignments[id], nil
}
func (f *fakeStore) UpdateAssignment(_ context.Context, a customfield.Assignment) error {
	f.assignments[a.ID] = a
	return nil
}
func (f *fakeStore) DeleteAssignment(_ context.Context, id uuid.UUID) error {
	if _, ok := f.assignments[id]; !ok {
		return fmt.Errorf("field assignment %s: %w", id, customfield.ErrNotFound)
	}
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

// SetValues follows the contract the real store has: check sees the
// definitions of the entries that name one, and nothing is written unless it
// returns nil.
func (f *fakeStore) SetValues(_ context.Context, ticketID uuid.UUID, entries map[uuid.UUID]string, check func(map[uuid.UUID]customfield.FieldDef) error) error {
	defs := map[uuid.UUID]customfield.FieldDef{}
	for id := range entries {
		if d, ok := f.defs[id]; ok {
			defs[id] = d
		}
	}
	if err := check(defs); err != nil {
		return err
	}
	for id, v := range entries {
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
