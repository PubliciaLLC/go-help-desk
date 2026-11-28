package customfield_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// An administrator can lower a field's limit below its type's (owner,
// 2026-11-11: "fixed caps but admin can lower it when they're defining the
// field"). The API holds every door to min(field, type); the store and the
// database know only the type's limit.

func ip(n int) *int { return &n }

func TestEffectiveMaxLength(t *testing.T) {
	for _, tc := range []struct {
		name string
		ft   customfield.FieldType
		max  *int
		want int
	}{
		{"text, none set", customfield.FieldTypeText, nil, 255},
		{"text, lowered", customfield.FieldTypeText, ip(100), 100},
		{"textarea, none set", customfield.FieldTypeTextarea, nil, 10_000},
		{"textarea, lowered", customfield.FieldTypeTextarea, ip(2000), 2000},
		{"select", customfield.FieldTypeSelect, nil, 255},
		{"number", customfield.FieldTypeNumber, nil, 255},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, customfield.FieldDef{FieldType: tc.ft, MaxLength: tc.max}.EffectiveMaxLength())
		})
	}
}

// Only a lower limit, and only on the two kinds of free text.
func TestCreateFieldDefLimited_Validation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		ft   customfield.FieldType
		opts []string
		max  *int
		ok   bool
	}{
		{"none", customfield.FieldTypeText, nil, nil, true},
		{"text 1", customfield.FieldTypeText, nil, ip(1), true},
		{"text 254", customfield.FieldTypeText, nil, ip(254), true},
		{"text at the cap", customfield.FieldTypeText, nil, ip(255), false},
		{"text over the cap", customfield.FieldTypeText, nil, ip(300), false},
		{"text zero", customfield.FieldTypeText, nil, ip(0), false},
		{"text negative", customfield.FieldTypeText, nil, ip(-5), false},
		{"textarea 9,999", customfield.FieldTypeTextarea, nil, ip(9999), true},
		{"textarea at the cap", customfield.FieldTypeTextarea, nil, ip(10_000), false},
		{"textarea may be lowered past the single-line cap", customfield.FieldTypeTextarea, nil, ip(5000), true},
		{"select", customfield.FieldTypeSelect, []string{"a"}, ip(10), false},
		{"number", customfield.FieldTypeNumber, nil, ip(10), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def, err := customfield.NewService(newFakeStore()).CreateFieldDefLimited(ctx, "F", tc.ft, tc.opts, 1, tc.max, nil)
			if tc.ok {
				require.NoError(t, err)
				require.Equal(t, tc.max, def.MaxLength)
				return
			}
			require.ErrorIs(t, err, customfield.ErrValidation)
		})
	}
}

func TestPatchFieldDef_MaxLength(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	def, err := svc.CreateFieldDef(ctx, "Log", customfield.FieldTypeTextarea, nil, 1, nil)
	require.NoError(t, err)

	got, err := svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{MaxLength: ip(2000)}, nil)
	require.NoError(t, err)
	require.Equal(t, ip(2000), got.MaxLength)

	got, err = svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{Name: new(string)}, nil)
	require.ErrorIs(t, err, customfield.ErrValidation, "an empty name, so nothing about the limit changed")
	_ = got

	name := "Renamed"
	got, err = svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{Name: &name}, nil)
	require.NoError(t, err)
	require.Equal(t, ip(2000), got.MaxLength, "a patch that does not mention it leaves it")

	_, err = svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{MaxLength: ip(10_000)}, nil)
	require.ErrorIs(t, err, customfield.ErrValidation, "not at the cap")
	require.Equal(t, ip(2000), store.defs[def.ID].MaxLength, "a refused change writes nothing")

	// Becoming single-line: the 2,000 is now above that type's cap.
	text := customfield.FieldTypeText
	_, err = svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{FieldType: &text}, nil)
	require.ErrorIs(t, err, customfield.ErrValidation)
	_, err = svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{FieldType: &text, MaxLength: ip(100)}, nil)
	require.NoError(t, err)

	got, err = svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{ClearMaxLength: true}, nil)
	require.NoError(t, err)
	require.Nil(t, got.MaxLength)
	require.Equal(t, 255, got.EffectiveMaxLength())

	sel := customfield.FieldTypeSelect
	_, err = svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{MaxLength: ip(50)}, nil)
	require.NoError(t, err)
	_, err = svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{FieldType: &sel, Options: []string{"a"}}, nil)
	require.ErrorIs(t, err, customfield.ErrValidation, "a select field has no limit of its own: clear it first")
}

func TestSetValues_HoldsTheFieldToItsOwnLimit(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	d, err := svc.CreateFieldDefLimited(ctx, "Short", customfield.FieldTypeText, nil, 1, ip(100), nil)
	require.NoError(t, err)
	tk := uuid.New()

	require.NoError(t, svc.SetValues(ctx, tk, map[uuid.UUID]string{d.ID: chars(100, "é")}), "exactly its own limit")
	err = svc.SetValues(ctx, tk, map[uuid.UUID]string{d.ID: chars(101, "é")})
	require.ErrorIs(t, err, customfield.ErrValidation)
	require.Contains(t, err.Error(), "100", "the message names the limit that applies")
	require.NotContains(t, err.Error(), "255")
}

// Every creation door asks the same check.
func TestOwnLimit_AtTheCreationDoors(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)
	d, err := f.svc.CreateFieldDefLimited(ctx, "Short", customfield.FieldTypeText, nil, 1, ip(100), nil)
	require.NoError(t, err)
	_, err = f.svc.Attach(ctx, customfield.Assignment{FieldDefID: d.ID, ScopeType: customfield.CategoryScope, ScopeID: f.cat, VisibleOnNew: true, VisibleToGuests: true}, nil)
	require.NoError(t, err)

	require.NoError(t, f.svc.CheckRequired(ctx, f.cat, nil, nil, map[string]string{d.ID.String(): chars(100, "a")}))
	err = f.svc.CheckRequired(ctx, f.cat, nil, nil, map[string]string{d.ID.String(): chars(101, "a")})
	require.ErrorIs(t, err, customfield.ErrValidation)
	require.Contains(t, err.Error(), "100")

	require.NoError(t, f.svc.CheckGuestValues(ctx, f.cat, map[string]string{d.ID.String(): chars(100, "a")}))
	require.ErrorIs(t, f.svc.CheckGuestValues(ctx, f.cat, map[string]string{d.ID.String(): chars(101, "a")}), customfield.ErrValidation)

	w, err := f.svc.SetCreationValues(ctx, uuid.New(), f.cat, nil, nil, map[string]string{d.ID.String(): chars(101, "a")})
	require.NoError(t, err)
	require.Equal(t, []customfield.Warning{{FieldID: d.ID.String(), Reason: customfield.ReasonTooLong}}, w)
}

// Lowering a limit never touches what is stored, and a value that does not
// change is not checked again.
func TestLoweredLimit_LeavesStoredValuesAndDoesNotRecheckAnUnchangedOne(t *testing.T) {
	ctx := context.Background()
	store := joiningFake{newFakeStore()}
	svc := customfield.NewService(store)
	d, err := svc.CreateFieldDef(ctx, "Note", customfield.FieldTypeText, nil, 1, nil)
	require.NoError(t, err)
	tk := uuid.New()
	old := chars(200, "a")
	require.NoError(t, svc.SetValues(ctx, tk, map[uuid.UUID]string{d.ID: old}))

	_, err = svc.PatchFieldDef(ctx, d.ID, customfield.FieldDefPatch{MaxLength: ip(100)}, nil)
	require.NoError(t, err, "lowering below an existing value is allowed")
	require.Equal(t, old, store.values[[2]uuid.UUID{tk, d.ID}], "and the value stays")

	require.NoError(t, svc.SetValues(ctx, tk, map[uuid.UUID]string{d.ID: old}), "the same value again is not re-checked")
	require.Equal(t, old, store.values[[2]uuid.UUID{tk, d.ID}])

	require.ErrorIs(t, svc.SetValues(ctx, tk, map[uuid.UUID]string{d.ID: chars(150, "b")}), customfield.ErrValidation,
		"a changed value is held to the new limit")
	require.NoError(t, svc.SetValues(ctx, tk, map[uuid.UUID]string{d.ID: chars(90, "b")}))
	require.ErrorIs(t, svc.SetValues(ctx, uuid.New(), map[uuid.UUID]string{d.ID: old}), customfield.ErrValidation,
		"on another ticket it is a new value")
}

func TestFieldDefJSON_CarriesTheEffectiveLimit(t *testing.T) {
	var out map[string]any
	b, err := json.Marshal(customfield.FieldDef{ID: uuid.New(), Name: "F", FieldType: customfield.FieldTypeText})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &out))
	require.EqualValues(t, 255, out["effective_max_length"])
	require.NotContains(t, out, "max_length", "unset is left out")

	b, _ = json.Marshal(customfield.FieldDef{FieldType: customfield.FieldTypeTextarea, MaxLength: ip(4000)})
	require.NoError(t, json.Unmarshal(b, &out))
	require.EqualValues(t, 4000, out["max_length"])
	require.EqualValues(t, 4000, out["effective_max_length"])

	b, _ = json.Marshal(customfield.FieldDef{FieldType: customfield.FieldTypeTextarea})
	require.NoError(t, json.Unmarshal(b, &out))
	require.EqualValues(t, 10_000, out["effective_max_length"])

	out = nil
	b, _ = json.Marshal(customfield.TicketFieldValue{FieldType: customfield.FieldTypeText, MaxLength: ip(50)})
	require.NoError(t, json.Unmarshal(b, &out))
	require.EqualValues(t, 50, out["effective_max_length"])
	require.NotContains(t, out, "max_length")
}

// The limit is part of the field's history.
func TestMaxLengthChangesAreAudited(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	actor := uuid.New()

	d, err := svc.CreateFieldDefLimited(ctx, "Short", customfield.FieldTypeText, nil, 1, ip(100), &actor)
	require.NoError(t, err)
	created := entriesByAction(store.audit, "custom_field_created")
	require.Len(t, created, 1)
	require.Equal(t, 100, created[0].After["max_length"])

	store.audit = nil
	_, err = svc.PatchFieldDef(ctx, d.ID, customfield.FieldDefPatch{MaxLength: ip(50)}, &actor)
	require.NoError(t, err)
	changed := entriesByAction(store.audit, "custom_field_max_length_changed")
	require.Len(t, changed, 1)
	require.Equal(t, map[string]any{"field_name": "Short", "max_length": 100}, changed[0].Before)
	require.Equal(t, map[string]any{"field_name": "Short", "max_length": 50}, changed[0].After)
	require.Equal(t, &actor, changed[0].ActorID)

	store.audit = nil
	_, err = svc.PatchFieldDef(ctx, d.ID, customfield.FieldDefPatch{ClearMaxLength: true}, &actor)
	require.NoError(t, err)
	changed = entriesByAction(store.audit, "custom_field_max_length_changed")
	require.Len(t, changed, 1)
	require.Equal(t, 50, changed[0].Before["max_length"])
	require.Equal(t, 255, changed[0].After["max_length"], "cleared reads as the type's limit")

	store.audit = nil
	_, err = svc.PatchFieldDef(ctx, d.ID, customfield.FieldDefPatch{ClearMaxLength: true}, &actor)
	require.NoError(t, err)
	require.Empty(t, store.audit, "no change, no entry")
}

func entriesByAction(all []audit.Entry, action string) []audit.Entry {
	var out []audit.Entry
	for _, e := range all {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

// joiningFake keeps its stored values where SetValues put them, so a test can
// say what a ticket holds.
func (f joiningFake) ListValuesForTicket(_ context.Context, ticket uuid.UUID) ([]customfield.TicketFieldValue, error) {
	var out []customfield.TicketFieldValue
	for k, v := range f.values {
		if k[0] == ticket {
			out = append(out, customfield.TicketFieldValue{TicketID: ticket, FieldDefID: k[1], Value: v})
		}
	}
	return out, nil
}
