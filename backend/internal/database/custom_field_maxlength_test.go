package database_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// A field's own limit on a value lives in the API and the forms (owner,
// 2026-11-11: "when we're writing it to the database, it's always at the fixed
// cap. The length limit is only enforced in the frontend, in the API."). The
// store holds the setting and knows only the type's limit.

func intp(n int) *int { return &n }

// allowAll is a decide that accepts what it is given: a write that does not go
// through the API's rules (an import, a migration, a direct store call).
func allowAll(entries map[uuid.UUID]string) func(map[uuid.UUID]customfield.FieldDef) (map[uuid.UUID]string, error) {
	return func(map[uuid.UUID]customfield.FieldDef) (map[uuid.UUID]string, error) { return entries, nil }
}

func TestStore_PersistsAFieldsOwnLimit(t *testing.T) {
	f := newLockFixture(t)
	ctx := context.Background()
	d, err := f.svc.CreateFieldDefLimited(ctx, "Short", customfield.FieldTypeText, nil, 1, intp(100), nil)
	require.NoError(t, err)

	got, err := f.svc.GetFieldDef(ctx, d.ID)
	require.NoError(t, err)
	require.Equal(t, intp(100), got.MaxLength)

	list, err := f.svc.ListFieldDefs(ctx)
	require.NoError(t, err)
	var listed customfield.FieldDef
	for _, l := range list {
		if l.ID == d.ID {
			listed = l
		}
	}
	require.Equal(t, intp(100), listed.MaxLength)

	patched, err := f.svc.PatchFieldDef(ctx, d.ID, customfield.FieldDefPatch{MaxLength: intp(50)}, nil)
	require.NoError(t, err)
	require.Equal(t, intp(50), patched.MaxLength)
	patched, err = f.svc.PatchFieldDef(ctx, d.ID, customfield.FieldDefPatch{ClearMaxLength: true}, nil)
	require.NoError(t, err)
	require.Nil(t, patched.MaxLength)

	// It travels with the field wherever the forms read it.
	_, err = f.svc.PatchFieldDef(ctx, d.ID, customfield.FieldDefPatch{MaxLength: intp(80)}, nil)
	require.NoError(t, err)
	var catID uuid.UUID
	require.NoError(t, f.db.SQL.QueryRowContext(ctx, `SELECT category_id FROM tickets WHERE id = $1`, f.ticketID).Scan(&catID))
	_, err = f.svc.Attach(ctx, customfield.Assignment{FieldDefID: d.ID, ScopeType: customfield.CategoryScope, ScopeID: catID, VisibleOnNew: true}, nil)
	require.NoError(t, err)
	as, err := f.svc.ListAssignments(ctx, customfield.CategoryScope, catID)
	require.NoError(t, err)
	require.Len(t, as, 1)
	require.Equal(t, intp(80), as[0].FieldDef.MaxLength)
	require.NoError(t, f.svc.SetValues(ctx, f.ticketID, map[uuid.UUID]string{d.ID: "ok"}))
	vals, err := f.svc.ListValuesForTicket(ctx, f.ticketID)
	require.NoError(t, err)
	require.Len(t, vals, 1)
	require.Equal(t, intp(80), vals[0].MaxLength)
}

// The same value: the store takes it at the type's cap whatever the field's own
// limit, and the API refuses it.
func TestStore_AcceptsTheTypeCapWhereTheAPIRefusesIt(t *testing.T) {
	f := newLockFixture(t)
	ctx := context.Background()
	short, err := f.svc.CreateFieldDefLimited(ctx, "Short", customfield.FieldTypeText, nil, 1, intp(100), nil)
	require.NoError(t, err)
	long, err := f.svc.CreateFieldDefLimited(ctx, "Log", customfield.FieldTypeTextarea, nil, 2, intp(2000), nil)
	require.NoError(t, err)
	atCap := map[uuid.UUID]string{short.ID: strings.Repeat("é", 255), long.ID: strings.Repeat("😀", 10_000)}

	// The API: 400.
	for id, v := range atCap {
		err := f.svc.SetValues(ctx, f.ticketID, map[uuid.UUID]string{id: v})
		require.ErrorIs(t, err, customfield.ErrValidation)
	}
	_, stored := f.stored(short.ID)
	require.False(t, stored)

	// The store, written to directly: accepted, and read back.
	require.NoError(t, f.store.SetValues(ctx, f.ticketID, atCap, allowAll(atCap)))
	got, ok := f.stored(short.ID)
	require.True(t, ok)
	require.Equal(t, atCap[short.ID], got)
	got, ok = f.stored(long.ID)
	require.True(t, ok)
	require.Equal(t, atCap[long.ID], got)
}

func TestStore_RefusesAValueAboveTheTypeCap(t *testing.T) {
	f := newLockFixture(t)
	ctx := context.Background()
	line := f.field("Line", customfield.FieldTypeText)
	long := f.field("Log", customfield.FieldTypeTextarea)

	for name, c := range map[string]struct {
		id    uuid.UUID
		value string
	}{
		"text, 256":        {line.ID, strings.Repeat("a", 256)},
		"textarea, 10,001": {long.ID, strings.Repeat("😀", 10_001)},
	} {
		t.Run(name, func(t *testing.T) {
			entries := map[uuid.UUID]string{c.id: c.value}
			err := f.store.SetValues(ctx, f.ticketID, entries, allowAll(entries))
			require.ErrorIs(t, err, customfield.ErrValidation)
			_, stored := f.stored(c.id)
			require.False(t, stored, "nothing written")
		})
	}
	t.Run("the deprecated direct write too", func(t *testing.T) {
		require.ErrorIs(t, f.store.UpsertValue(ctx, f.ticketID, line.ID, strings.Repeat("a", 256)), customfield.ErrValidation)
		require.NoError(t, f.store.UpsertValue(ctx, f.ticketID, line.ID, strings.Repeat("a", 255)))
	})
	t.Run("one bad entry takes the batch with it", func(t *testing.T) {
		entries := map[uuid.UUID]string{line.ID: "fine", long.ID: strings.Repeat("a", 10_001)}
		require.Error(t, f.store.SetValues(ctx, f.ticketID, entries, allowAll(entries)))
		got, _ := f.stored(line.ID)
		require.Equal(t, strings.Repeat("a", 255), got, "the earlier value is as it was")
	})
}
