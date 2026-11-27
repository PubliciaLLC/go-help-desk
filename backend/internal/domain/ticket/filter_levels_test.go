package ticket

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/catalogue"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The filter on an item (or a type) is refused to a caller who cannot see that
// level, as every other unavailable filter is.
func TestAllowedForLevels(t *testing.T) {
	item := mustParse(t, "item_id=2b2fd5de-6b77-4a43-a0f1-0b9f0d2a4f10")
	typ := mustParse(t, "type_id=2b2fd5de-6b77-4a43-a0f1-0b9f0d2a4f10")
	plain := mustParse(t, "category_id=2b2fd5de-6b77-4a43-a0f1-0b9f0d2a4f10")
	noItems := catalogue.Levels{Types: true}

	for _, role := range []user.Role{user.RoleUser, user.Role("")} {
		err := item.AllowedForLevels(role, noItems, false)
		var fe *FilterError
		require.ErrorAs(t, err, &fe)
		require.Equal(t, FilterForbidden, fe.Kind)
		require.Contains(t, fe.Msg, "item_id")
		require.NoError(t, item.AllowedForLevels(role, catalogue.Levels{Types: true, Items: true}, false), "allowed once Items is visible")
		require.NoError(t, typ.AllowedForLevels(role, noItems, false), "the type filter stays while Types is visible")
		require.ErrorAs(t, typ.AllowedForLevels(role, catalogue.Levels{}, false), &fe)
		require.Contains(t, fe.Msg, "type_id")
		require.NoError(t, plain.AllowedForLevels(role, noItems, false))
	}
	for _, role := range []user.Role{user.RoleStaff, user.RoleAdmin} {
		require.NoError(t, item.AllowedForLevels(role, catalogue.Levels{}, false), "staff are never narrowed")
	}
	// It still runs every check AllowedFor does.
	require.Error(t, mustParse(t, "reporter_id=2b2fd5de-6b77-4a43-a0f1-0b9f0d2a4f10").AllowedForLevels(user.RoleUser, catalogue.Levels{Types: true, Items: true}, false))
}
