package customfield_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// joiningFake is fakeStore with ListAssignments answering as the real store
// does: each assignment carries its definition, which the rules in this file
// read (is the field active, what are its options).
type joiningFake struct{ *fakeStore }

func (f joiningFake) ListAssignments(ctx context.Context, st customfield.ScopeType, id uuid.UUID) ([]customfield.Assignment, error) {
	out, err := f.fakeStore.ListAssignments(ctx, st, id)
	for i := range out {
		d := f.defs[out[i].FieldDefID]
		out[i].FieldDef = &d
	}
	return out, err
}

type rulesFixture struct {
	t     *testing.T
	store joiningFake
	svc   *customfield.Service
	cat   uuid.UUID
}

func newRulesFixture(t *testing.T) *rulesFixture {
	t.Helper()
	store := joiningFake{newFakeStore()}
	return &rulesFixture{t: t, store: store, svc: customfield.NewService(store), cat: uuid.New()}
}

// field creates a definition and attaches it at scope. guests is whether the
// attachment is offered to guests.
func (f *rulesFixture) field(name string, ft customfield.FieldType, opts []string, scope customfield.ScopeType, scopeID uuid.UUID, visible, required, guests bool) customfield.FieldDef {
	f.t.Helper()
	ctx := context.Background()
	d, err := f.svc.CreateFieldDef(ctx, name, ft, opts, 1, nil)
	require.NoError(f.t, err)
	_, err = f.svc.Attach(ctx, customfield.Assignment{
		FieldDefID: d.ID, ScopeType: scope, ScopeID: scopeID, SortOrder: 1,
		VisibleOnNew: visible, RequiredOnNew: required, VisibleToGuests: guests,
	}, nil)
	require.NoError(f.t, err)
	return d
}

func (f *rulesFixture) deactivate(d customfield.FieldDef) {
	d.Active = false
	f.store.defs[d.ID] = d
}

// #626(a): "required" is a rule of the server, not of the form.
func TestCheckRequired(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)
	typ, item := uuid.New(), uuid.New()

	serial := f.field("Serial", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, true, false)
	optional := f.field("Notes", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, false, false)
	hidden := f.field("Internal ref", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, false, true, false)
	gone := f.field("Old", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, true, false)
	f.deactivate(gone)
	pick := f.field("Pick", customfield.FieldTypeSelect, []string{"a", "b"}, customfield.TypeScope, typ, true, true, false)
	cost := f.field("Cost", customfield.FieldTypeNumber, nil, customfield.ItemScope, item, true, true, false)

	all := map[string]string{
		serial.ID.String(): "SN-1", pick.ID.String(): "a", cost.ID.String(): "12",
	}
	with := func(m map[string]string, k, v string) map[string]string {
		out := map[string]string{}
		for kk, vv := range m {
			out[kk] = vv
		}
		out[k] = v
		return out
	}
	without := func(m map[string]string, k string) map[string]string {
		out := with(m, k, "")
		delete(out, k)
		return out
	}

	for _, tc := range []struct {
		name     string
		typeID   *uuid.UUID
		itemID   *uuid.UUID
		raw      map[string]string
		wantErr  bool
		wantText []string // each must be in the message
	}{
		{"every required field has a value", &typ, &item, all, false, nil},
		{"a required field with no entry", &typ, &item, without(all, serial.ID.String()), true, []string{"Serial"}},
		{"an empty value is no value", &typ, &item, with(all, serial.ID.String(), ""), true, []string{"Serial"}},
		{"spaces are no value", &typ, &item, with(all, serial.ID.String(), "   "), true, []string{"Serial"}},
		{"every missing field is named", &typ, &item, map[string]string{}, true, []string{"Serial", "Pick", "Cost"}},
		{"a field not shown on the form is not required", &typ, &item, all, false, nil},
		{"an optional field may be left out", &typ, &item, without(with(all, optional.ID.String(), "x"), optional.ID.String()), false, nil},
		{"a deactivated field is not required", &typ, &item, all, false, nil},
		{"a type's field is not required on a ticket without that type", nil, nil, map[string]string{serial.ID.String(): "SN-1"}, false, nil},
		{"an item's field is not required without the item", &typ, nil, without(all, cost.ID.String()), false, nil},
		{"a required select needs an option in use", &typ, &item, with(all, pick.ID.String(), "zzz"), true, []string{"Pick"}},
		{"a required number needs a number", &typ, &item, with(all, cost.ID.String(), "abc"), true, []string{"Cost"}},
		{"the key may spell the id in upper case", &typ, &item,
			with(without(all, serial.ID.String()), strings.ToUpper(serial.ID.String()), "SN-1"), false, nil},
		{"a field named twice is refused, not guessed at", &typ, &item,
			with(all, strings.ToUpper(serial.ID.String()), "other"), true, []string{"Serial"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := f.svc.CheckRequired(ctx, f.cat, tc.typeID, tc.itemID, tc.raw)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, customfield.ErrValidation)
			for _, s := range tc.wantText {
				require.Contains(t, err.Error(), s)
			}
		})
	}
	_ = hidden
}

// The most specific attachment decides, as on the form: a field required at the
// category but not at the item is not required on a ticket that has the item.
func TestCheckRequired_MostSpecificAttachmentDecides(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)
	typ := uuid.New()
	d := f.field("Serial", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, true, false)
	_, err := f.svc.Attach(ctx, customfield.Assignment{
		FieldDefID: d.ID, ScopeType: customfield.TypeScope, ScopeID: typ, VisibleOnNew: true, RequiredOnNew: false,
	}, nil)
	require.NoError(t, err)

	require.NoError(t, f.svc.CheckRequired(ctx, f.cat, &typ, nil, nil), "the type's settings apply")
	require.ErrorIs(t, f.svc.CheckRequired(ctx, f.cat, nil, nil, nil), customfield.ErrValidation, "without the type, the category's")
}

// #626(b): blank and repeated options.
func TestFieldDef_RefusesBlankAndDuplicateOptions(t *testing.T) {
	ctx := context.Background()
	strs := func(s ...string) []string { return append([]string{}, s...) }

	t.Run("on create", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			opts []string
			ok   bool
		}{
			{"clean", strs("a", "b"), true},
			{"blank", strs("a", ""), false},
			{"spaces", strs("a", "  "), false},
			{"repeated", strs("a", "b", "a"), false},
			{"differs only in case", strs("a", "A"), true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := customfield.NewService(newFakeStore()).CreateFieldDef(ctx, "Pick", customfield.FieldTypeSelect, tc.opts, 1, nil)
				if tc.ok {
					require.NoError(t, err)
					return
				}
				require.ErrorIs(t, err, customfield.ErrValidation)
			})
		}
	})

	t.Run("on patch", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			options []string // stored before
			retired []string
			patch   customfield.FieldDefPatch
			ok      bool
		}{
			{"a blank option is added", strs("a"), nil, customfield.FieldDefPatch{Options: strs("a", " ")}, false},
			{"a repeated option is added", strs("a"), nil, customfield.FieldDefPatch{Options: strs("a", "a")}, false},
			{"a retired option's text is repeated in use is already refused", strs("a"), strs("b"), customfield.FieldDefPatch{Options: strs("a", "b")}, false},
			{"a blank retired option", strs("a", "b"), nil, customfield.FieldDefPatch{Options: strs("a"), RetiredOptions: strs("b", "")}, false},
			{"a repeated retired option", strs("a", "b"), nil, customfield.FieldDefPatch{Options: strs("a"), RetiredOptions: strs("b", "b")}, false},
			{"a clean change", strs("a"), nil, customfield.FieldDefPatch{Options: strs("a", "b")}, true},
			// The lists are checked only when a patch changes them, so a field
			// 1.2.0 or the API left with a blank option still saves a rename.
			{"a rename of a field whose options were already bad", strs("a", "", "a"), nil,
				customfield.FieldDefPatch{Name: new(string), SortOrder: new(int)}, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				store := newFakeStore()
				svc := customfield.NewService(store)
				def, err := svc.CreateFieldDef(ctx, "Pick", customfield.FieldTypeSelect, strs("seed"), 1, nil)
				require.NoError(t, err)
				def.Options, def.RetiredOptions = tc.options, tc.retired
				store.defs[def.ID] = def
				if tc.patch.Name != nil {
					*tc.patch.Name = "Renamed"
				}

				_, err = svc.PatchFieldDef(ctx, def.ID, tc.patch, nil)
				if tc.ok {
					require.NoError(t, err)
					return
				}
				require.ErrorIs(t, err, customfield.ErrValidation)
				require.Equal(t, def, store.defs[def.ID], "a refused change writes nothing")
			})
		}
	})
}

// A field offered to guests is a category-level attachment: a guest picks a
// category and nothing below it, so a flag anywhere else would promise
// something no guest can receive.
func TestAttach_GuestFlagIsForCategoryAttachmentsOnly(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)
	d, err := f.svc.CreateFieldDef(ctx, "Serial", customfield.FieldTypeText, nil, 1, nil)
	require.NoError(t, err)

	_, err = f.svc.Attach(ctx, customfield.Assignment{FieldDefID: d.ID, ScopeType: customfield.TypeScope, ScopeID: uuid.New(), VisibleToGuests: true}, nil)
	require.ErrorIs(t, err, customfield.ErrValidation)
	_, err = f.svc.Attach(ctx, customfield.Assignment{FieldDefID: d.ID, ScopeType: customfield.ItemScope, ScopeID: uuid.New(), VisibleToGuests: true}, nil)
	require.ErrorIs(t, err, customfield.ErrValidation)
	_, err = f.svc.Attach(ctx, customfield.Assignment{FieldDefID: d.ID, ScopeType: customfield.CategoryScope, ScopeID: f.cat, VisibleToGuests: true}, nil)
	require.NoError(t, err)

	// And an update cannot move the flag onto one.
	typed, err := f.svc.Attach(ctx, customfield.Assignment{FieldDefID: d.ID, ScopeType: customfield.TypeScope, ScopeID: uuid.New()}, nil)
	require.NoError(t, err)
	typed.VisibleToGuests = true
	require.ErrorIs(t, f.svc.UpdateAssignment(ctx, typed, nil), customfield.ErrValidation)
}

// What a guest is offered, and nothing else.
func TestGuestFields_OnlyWhatTheOperatorOffered(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)
	other := uuid.New()

	offered := f.field("Serial", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, true, true)
	f.field("Staff only", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, false, false)
	f.field("Hidden", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, false, false, true) // flagged but not on the form
	gone := f.field("Old", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, false, true)
	f.deactivate(gone)
	f.field("Elsewhere", customfield.FieldTypeText, nil, customfield.CategoryScope, other, true, false, true)

	got, err := f.svc.GuestFields(ctx, f.cat)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, offered.ID, got[0].FieldDefID)
}

func TestCheckGuestValues(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)

	serial := f.field("Serial", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, true, true)
	pick := f.field("Pick", customfield.FieldTypeSelect, []string{"a", "b"}, customfield.CategoryScope, f.cat, true, false, true)
	staffOnly := f.field("Staff only", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, false, false)
	hiddenRequired := f.field("Hidden", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, true, false) // required for the form, not offered to guests
	retiredPick := f.field("Retired pick", customfield.FieldTypeSelect, []string{"x", "y"}, customfield.CategoryScope, f.cat, true, false, true)
	d := f.store.defs[retiredPick.ID]
	d.Options, d.RetiredOptions = []string{"x"}, []string{"y"}
	f.store.defs[d.ID] = d

	good := map[string]string{serial.ID.String(): "SN-1", pick.ID.String(): "b"}

	for _, tc := range []struct {
		name string
		raw  map[string]string
		want string // "" = accepted
	}{
		{"offered fields, valid", good, ""},
		{"no values at all, but a required one is offered", nil, "Serial"},
		{"an optional field may be left out", map[string]string{serial.ID.String(): "SN-1"}, ""},
		{"a field staff fill is refused", map[string]string{serial.ID.String(): "SN-1", staffOnly.ID.String(): "x"}, "not offered"},
		{"a field staff fill is refused even when empty", map[string]string{serial.ID.String(): "SN-1", staffOnly.ID.String(): ""}, "not offered"},
		{"a field that does not exist", map[string]string{serial.ID.String(): "SN-1", uuid.NewString(): "x"}, "not offered"},
		{"a key that is not an id", map[string]string{serial.ID.String(): "SN-1", "../etc": "x"}, "not offered"},
		{"an option outside the list", map[string]string{serial.ID.String(): "SN-1", pick.ID.String(): "zzz"}, "Pick"},
		{"a retired option", map[string]string{serial.ID.String(): "SN-1", retiredPick.ID.String(): "y"}, "Retired pick"},
		{"a field the form requires but guests are not offered is not required of them", good, ""},
		{"spaces do not satisfy a required field", map[string]string{serial.ID.String(): "  "}, "Serial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := f.svc.CheckGuestValues(ctx, f.cat, tc.raw)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, customfield.ErrValidation)
			require.Contains(t, err.Error(), tc.want)
			// A refusal never carries the name of a field the guest was not offered.
			require.NotContains(t, err.Error(), "Staff only")
			require.NotContains(t, err.Error(), "Hidden")
		})
	}
	_ = hiddenRequired
}

// The write is held to the offered set as well, so a value for a field that was
// withdrawn between the check and the write is dropped, not stored.
func TestSetGuestCreationValues_WritesOnlyWhatIsOffered(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)
	offered := f.field("Serial", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, false, true)
	staffOnly := f.field("Staff only", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, false, false)

	tk := uuid.New()
	warnings, err := f.svc.SetGuestCreationValues(ctx, tk, f.cat, map[string]string{
		offered.ID.String(): "SN-1", staffOnly.ID.String(): "sneaky",
	})
	require.NoError(t, err)
	require.Equal(t, "SN-1", f.store.values[[2]uuid.UUID{tk, offered.ID}])
	require.NotContains(t, f.store.values, [2]uuid.UUID{tk, staffOnly.ID}, "a field not offered to guests is never written")
	require.Equal(t, []customfield.Warning{{FieldID: staffOnly.ID.String(), Reason: customfield.ReasonNotOnTicket}}, warnings)
}
