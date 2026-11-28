package database_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// A long-text value of 10,000 characters has to store, in any script: the
// owner's limit (2026-11-11) is in characters, and four-byte characters make
// that 40 KB. Migration 000009 indexed (field_def_id, value), and Postgres
// refuses an index row over about 2,704 bytes, so until migration 000065 changed
// the index a value over about 2.7 KB of ordinary text could not be saved at all.

func TestLongValues_TenThousandMultiByteCharactersStoreAndReadBack(t *testing.T) {
	f := newLockFixture(t)
	ctx := context.Background()
	long := f.field("Log", customfield.FieldTypeTextarea)

	for name, value := range map[string]string{
		// Varied text, because a repeated character compresses and the index
		// limit is hit by text that does not.
		"four-byte":  randRunes(10_000, 0x1F300, 0x1F64F),
		"three-byte": randRunes(10_000, 0x4E00, 0x9FFF),
		"mixed":      randRunes(10_000, 0x20, 0x9FFF),
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, f.svc.SetValues(ctx, f.ticketID, map[uuid.UUID]string{long.ID: value}))
			got, err := f.svc.ListValuesForTicket(ctx, f.ticketID)
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Equal(t, value, got[0].Value, "read back exactly")
		})
	}

	t.Run("ten thousand and one is refused, and nothing is written", func(t *testing.T) {
		before, _ := f.stored(long.ID)
		err := f.svc.SetValues(ctx, f.ticketID, map[uuid.UUID]string{long.ID: strings.Repeat("😀", 10_001)})
		require.ErrorIs(t, err, customfield.ErrValidation)
		after, _ := f.stored(long.ID)
		require.Equal(t, before, after)
	})
}

// The queries that used the index still answer: a select value is held, found
// by value, counted by value.
func TestLongValues_ValueQueriesStillWork(t *testing.T) {
	f := newLockFixture(t)
	ctx := context.Background()
	sel := f.field("Brand", customfield.FieldTypeSelect, "Dell", strings.Repeat("é", 255))
	require.NoError(t, f.svc.SetValues(ctx, f.ticketID, map[uuid.UUID]string{sel.ID: strings.Repeat("é", 255)}))

	// Removing the option a ticket holds is refused: this is FirstHeldCustomFieldValue.
	_, err := f.svc.PatchFieldDef(ctx, sel.ID, customfield.FieldDefPatch{Options: []string{"Dell"}}, nil)
	require.ErrorIs(t, err, customfield.ErrConflict)

	u, err := f.svc.Usage(ctx, sel.ID)
	require.NoError(t, err)
	require.Equal(t, 1, u.TicketsWithValue)
	var held int
	for _, v := range u.Values {
		if v.Value == strings.Repeat("é", 255) {
			held = v.Tickets
		}
	}
	require.Equal(t, 1, held)
}

// Migration 000065's down puts back the index 1.2.0 had, which cannot hold a
// long value: values over 2,600 bytes are cut to fit, as 1.2.0 could never have
// stored them either. The up leaves values and index as they were.
func TestMigration065_DownCutsWhatTheOldIndexCannotHoldAndUpRestoresTheIndex(t *testing.T) {
	f := newLockFixture(t)
	ctx := context.Background()
	long := f.field("Log", customfield.FieldTypeTextarea)
	short := f.field("Note", customfield.FieldTypeText)
	huge := strings.Repeat("😀", 10_000)
	require.NoError(t, f.svc.SetValues(ctx, f.ticketID, map[uuid.UUID]string{long.ID: huge, short.ID: "kept"}))

	tx, err := f.db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	indexDef := func() string {
		var def string
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_cfv_field_def'`).Scan(&def))
		return def
	}
	require.Contains(t, indexDef(), `"left"(value, 255)`, "up: the index holds a prefix")

	execMigrationFile(t, ctx, tx, "migrations/000065_custom_field_guest_visible.down.sql", nil)
	require.NotContains(t, indexDef(), "left", "down: the original index is back")
	var n int
	var gotShort string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT octet_length(value) FROM ticket_custom_field_values WHERE field_def_id = $1`, long.ID).Scan(&n))
	require.LessOrEqual(t, n, 2600, "a value the old index cannot hold was cut to fit")
	require.Positive(t, n)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT value FROM ticket_custom_field_values WHERE field_def_id = $1`, short.ID).Scan(&gotShort))
	require.Equal(t, "kept", gotShort, "a value that fits is not touched")

	execMigrationFile(t, ctx, tx, "migrations/000065_custom_field_guest_visible.up.sql", nil)
	require.Contains(t, indexDef(), `"left"(value, 255)`)
}

// randRunes is n characters from lo to hi, the same every run.
func randRunes(n int, lo, hi rune) string {
	var b strings.Builder
	x := uint32(1)
	for i := 0; i < n; i++ {
		x = x*1664525 + 1013904223
		r := lo + rune(x>>8)%(hi-lo+1)
		if r >= 0xD800 && r <= 0xDFFF {
			r = lo
		}
		b.WriteRune(r)
	}
	return b.String()
}
