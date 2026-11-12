package customfield_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// A select field saved before the limits can hold an option over 255 characters.
// It can no longer be chosen, and the administrator has to be able to shorten it:
// the length rule is for options that are new, not for what is already there, or
// no edit to the list (a rename included) could ever be saved.
func TestLegacyLongOption_CanBeShortenedAndDoesNotBlockOtherEdits(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := customfield.NewService(store)
	def, err := svc.CreateFieldDef(ctx, "Pick", customfield.FieldTypeSelect, []string{"short"}, 1, nil)
	require.NoError(t, err)
	legacy := chars(300, "a")
	def.Options = []string{"short", legacy} // as a 1.3.0-beta database holds it
	store.defs[def.ID] = def

	t.Run("adding another option is not refused over the old one", func(t *testing.T) {
		_, err := svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{Options: []string{"short", legacy, "new"}}, nil)
		require.NoError(t, err)
	})
	t.Run("a new option over 255 is refused", func(t *testing.T) {
		_, err := svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{Options: []string{"short", legacy, "new", chars(256, "b")}}, nil)
		require.ErrorIs(t, err, customfield.ErrValidation)
	})
	t.Run("renaming it to something short retires the long one, and that saves", func(t *testing.T) {
		got, err := svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{
			Options: []string{"short", "new", "shortened"}, RetiredOptions: []string{legacy},
		}, nil)
		require.NoError(t, err)
		require.Equal(t, []string{legacy}, got.RetiredOptions)
	})
	t.Run("a retired long option does not block the next edit", func(t *testing.T) {
		_, err := svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{Options: []string{"short", "new", "shortened", "more"}}, nil)
		require.NoError(t, err)
	})
	t.Run("deleting it, when nothing holds it, saves", func(t *testing.T) {
		_, err := svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{RetiredOptions: []string{}}, nil)
		require.NoError(t, err)
	})
}
