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
}

// Category fields come first, then type, then item: the order the new-ticket
// form shows them in.
func TestResolveFieldsForCTI_OrdersByScopeLevel(t *testing.T) {
	ctx := context.Background()
	svc := customfield.NewService(newFakeStore())
	cat, typ, item := uuid.New(), uuid.New(), uuid.New()
	def := uuid.New()

	ia, err := svc.CreateAssignment(ctx, def, customfield.ItemScope, item, 1, true, false)
	require.NoError(t, err)
	ca, err := svc.CreateAssignment(ctx, def, customfield.CategoryScope, cat, 1, true, false)
	require.NoError(t, err)
	ta, err := svc.CreateAssignment(ctx, def, customfield.TypeScope, typ, 1, true, false)
	require.NoError(t, err)

	got, err := svc.ResolveFieldsForCTI(ctx, cat, &typ, &item)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{ca.ID, ta.ID, ia.ID}, []uuid.UUID{got[0].ID, got[1].ID, got[2].ID})

	got, err = svc.ResolveFieldsForCTI(ctx, cat, nil, nil)
	require.NoError(t, err)
	require.Len(t, got, 1, "no type or item means only the category's fields")
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
