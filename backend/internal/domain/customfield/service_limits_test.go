package customfield_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// A value has a longest length, counted in characters and not bytes, so every
// door refuses what it cannot store (owner, 2026-11-11: single-line text 255,
// long text 10,000).

func chars(n int, r string) string { return strings.Repeat(r, n) }

func TestValueLimits_ByFieldType(t *testing.T) {
	require.Equal(t, 255, customfield.MaxTextChars)
	require.Equal(t, 10_000, customfield.MaxLongTextChars)

	for _, tc := range []struct {
		ft    customfield.FieldType
		limit int
	}{
		{customfield.FieldTypeText, 255},
		{customfield.FieldTypeTextarea, 10_000},
		{customfield.FieldTypeNumber, 255},
		{customfield.FieldTypeSelect, 255},
	} {
		require.Equal(t, tc.limit, customfield.MaxChars(tc.ft), string(tc.ft))
	}
}

func TestSetValues_RefusesAValueOverTheLimit(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		ft    customfield.FieldType
		limit int
		unit  string
	}{
		{"text, ascii", customfield.FieldTypeText, 255, "a"},
		{"text, two-byte characters", customfield.FieldTypeText, 255, "é"},
		{"text, four-byte characters", customfield.FieldTypeText, 255, "😀"},
		{"textarea, ascii", customfield.FieldTypeTextarea, 10_000, "a"},
		{"textarea, three-byte characters", customfield.FieldTypeTextarea, 10_000, "中"},
		{"textarea, four-byte characters", customfield.FieldTypeTextarea, 10_000, "😀"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			svc := customfield.NewService(store)
			d, err := svc.CreateFieldDef(ctx, "F", tc.ft, nil, 1, nil)
			require.NoError(t, err)
			tk := uuid.New()

			require.NoError(t, svc.SetValues(ctx, tk, map[uuid.UUID]string{d.ID: chars(tc.limit, tc.unit)}),
				"exactly the limit, in characters, is accepted whatever it weighs in bytes")
			require.Len(t, []rune(store.values[[2]uuid.UUID{tk, d.ID}]), tc.limit)

			err = svc.SetValues(ctx, tk, map[uuid.UUID]string{d.ID: chars(tc.limit+1, tc.unit)})
			require.ErrorIs(t, err, customfield.ErrValidation)
			require.Less(t, len(err.Error()), 200, "the refusal does not echo the value")
			require.Contains(t, err.Error(), strconv.Itoa(tc.limit))
		})
	}
}

func TestSetValues_NumberAndSelectAreHeldTo255(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	num, err := svc.CreateFieldDef(ctx, "N", customfield.FieldTypeNumber, nil, 1, nil)
	require.NoError(t, err)
	// A number this long is a number, and is still refused.
	long := "1" + chars(255, "0")
	require.ErrorIs(t, svc.SetValues(ctx, uuid.New(), map[uuid.UUID]string{num.ID: long}), customfield.ErrValidation)
	require.NoError(t, svc.SetValues(ctx, uuid.New(), map[uuid.UUID]string{num.ID: "  " + chars(255, "1") + "  "}),
		"spaces around the number are not counted: it is trimmed first")
}

// Options are held to the same limit as a select value, or an administrator
// could make an option nobody can choose.
func TestFieldDef_RefusesAnOptionOver255Characters(t *testing.T) {
	ctx := context.Background()
	svc := customfield.NewService(newFakeStore())
	_, err := svc.CreateFieldDef(ctx, "Pick", customfield.FieldTypeSelect, []string{"a", chars(255, "é")}, 1, nil)
	require.NoError(t, err)
	_, err = svc.CreateFieldDef(ctx, "Pick2", customfield.FieldTypeSelect, []string{"a", chars(256, "é")}, 1, nil)
	require.ErrorIs(t, err, customfield.ErrValidation)
}

// Creation refuses before the ticket exists, for fields that are not required
// too: a value the write would refuse is a 400 up front, not a warning after.
func TestCheckRequired_RefusesAValueOverTheLimitOnAnyField(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)
	optional := f.field("Notes", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, false, false)
	long := f.field("Log", customfield.FieldTypeTextarea, nil, customfield.CategoryScope, f.cat, true, false, false)

	require.NoError(t, f.svc.CheckRequired(ctx, f.cat, nil, nil, map[string]string{
		optional.ID.String(): chars(255, "é"), long.ID.String(): chars(10_000, "😀"),
	}))
	err := f.svc.CheckRequired(ctx, f.cat, nil, nil, map[string]string{optional.ID.String(): chars(256, "a")})
	require.ErrorIs(t, err, customfield.ErrValidation)
	require.Contains(t, err.Error(), "Notes")
	require.ErrorIs(t, f.svc.CheckRequired(ctx, f.cat, nil, nil, map[string]string{long.ID.String(): chars(10_001, "a")}), customfield.ErrValidation)
}

func TestCheckGuestValues_RefusesAValueOverTheLimit(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)
	d := f.field("Log", customfield.FieldTypeTextarea, nil, customfield.CategoryScope, f.cat, true, false, true)
	require.NoError(t, f.svc.CheckGuestValues(ctx, f.cat, map[string]string{d.ID.String(): chars(10_000, "中")}))
	require.ErrorIs(t, f.svc.CheckGuestValues(ctx, f.cat, map[string]string{d.ID.String(): chars(10_001, "中")}), customfield.ErrValidation)
}

// A guest cannot tell a retired option from one that never existed.
func TestCheckGuestValues_RetiredOptionReadsLikeAnUnknownOne(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)
	d := f.field("Colour", customfield.FieldTypeSelect, []string{"red", "blue"}, customfield.CategoryScope, f.cat, true, false, true)
	cur := f.store.defs[d.ID]
	cur.Options, cur.RetiredOptions = []string{"red"}, []string{"blue"}
	f.store.defs[d.ID] = cur

	retired := f.svc.CheckGuestValues(ctx, f.cat, map[string]string{d.ID.String(): "blue"})
	unknown := f.svc.CheckGuestValues(ctx, f.cat, map[string]string{d.ID.String(): "purple"})
	require.ErrorIs(t, retired, customfield.ErrValidation)
	require.ErrorIs(t, unknown, customfield.ErrValidation)
	require.Equal(t, strings.ReplaceAll(unknown.Error(), "purple", "blue"), retired.Error())
	require.NotContains(t, retired.Error(), "retired")
}

// What the creation write reports for the one thing that gets past the check.
func TestSetCreationValues_OverTheLimitIsAWarningNotAWrite(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)
	d := f.field("Notes", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, false, false)
	tk := uuid.New()
	w, err := f.svc.SetCreationValues(ctx, tk, f.cat, nil, nil, map[string]string{d.ID.String(): chars(256, "a")})
	require.NoError(t, err)
	require.Equal(t, []customfield.Warning{{FieldID: d.ID.String(), Reason: customfield.ReasonTooLong}}, w)
	require.Empty(t, f.store.values)
}

// CheckCreation says whether the classification has a required field, which is
// what decides whether a failed value write fails the request.
func TestCheckCreation_ReportsWhetherAFieldIsRequired(t *testing.T) {
	ctx := context.Background()
	f := newRulesFixture(t)
	f.field("Notes", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, false, false)
	has, err := f.svc.CheckCreation(ctx, f.cat, nil, nil, nil)
	require.NoError(t, err)
	require.False(t, has)
	f.field("Hidden", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, false, true, false)
	has, _ = f.svc.CheckCreation(ctx, f.cat, nil, nil, nil)
	require.False(t, has, "a field that is not shown on the form is not required")
	serial := f.field("Serial", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, true, false)
	has, err = f.svc.CheckCreation(ctx, f.cat, nil, nil, map[string]string{serial.ID.String(): "x"})
	require.NoError(t, err)
	require.True(t, has, "said even when the request satisfies it")
	has, err = f.svc.CheckCreation(ctx, f.cat, nil, nil, nil)
	require.ErrorIs(t, err, customfield.ErrValidation)
	require.True(t, has)
}
