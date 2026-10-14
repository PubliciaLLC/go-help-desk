package customfield_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// The caller's mistakes are ErrValidation, so the HTTP layer can tell them from
// a failed database call, and the messages they carry are unchanged.
func TestService_CallerMistakesAreErrValidation(t *testing.T) {
	ctx := context.Background()
	svc := customfield.NewService(newFakeStore())
	sel, err := svc.CreateFieldDef(ctx, "Pick", customfield.FieldTypeSelect, []string{"a"}, 1)
	require.NoError(t, err)
	off := customfield.FieldDef{ID: uuid.New(), Name: "Off", FieldType: customfield.FieldTypeText}

	cases := []struct {
		name string
		call func() error
		msg  string
	}{
		{"blank name on create", func() error { _, err := svc.CreateFieldDef(ctx, " ", customfield.FieldTypeText, nil, 1); return err },
			"field name is required"},
		{"unknown type on create", func() error { _, err := svc.CreateFieldDef(ctx, "x", "colour", nil, 1); return err },
			`invalid field type "colour"`},
		{"select without options on create", func() error { _, err := svc.CreateFieldDef(ctx, "x", customfield.FieldTypeSelect, nil, 1); return err },
			"select fields require at least one option"},
		{"blank name on update", func() error { d := sel; d.Name = " "; return svc.UpdateFieldDef(ctx, d) },
			"field name is required"},
		{"value outside the options", func() error { return svc.SetValue(ctx, uuid.New(), sel.ID, "zzz") },
			`value "zzz" is not a valid option for field "Pick"`},
		{"inactive field", func() error {
			store := newFakeStore()
			store.defs[off.ID] = off
			return customfield.NewService(store).SetValue(ctx, uuid.New(), off.ID, "v")
		}, `field "Off" is deactivated`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			require.ErrorIs(t, err, customfield.ErrValidation)
			require.EqualError(t, err, tc.msg)
		})
	}
}

func TestStoreErrorConstructors_AnswerTheirSentinelAndNothingElse(t *testing.T) {
	c, r := customfield.Conflict("taken"), customfield.InvalidReference("no such field")
	require.ErrorIs(t, c, customfield.ErrConflict)
	require.NotErrorIs(t, c, customfield.ErrInvalidReference)
	require.NotErrorIs(t, c, customfield.ErrValidation)
	require.ErrorIs(t, r, customfield.ErrInvalidReference)
	require.NotErrorIs(t, r, customfield.ErrConflict)
	require.EqualError(t, c, "taken")
	require.EqualError(t, r, "no such field")
}
