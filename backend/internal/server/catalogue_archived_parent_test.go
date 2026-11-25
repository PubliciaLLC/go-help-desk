package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// An archived parent takes its children out of what non-staff are offered.
// Archiving does not cascade (a child keeps active=true), so the list for an
// archived category used to hand a guest or a requester its active types, and
// now their descriptions, if they knew the id. Staff still see the whole tree.
func TestCatalogue_ArchivedParentServesNoChildrenToNonStaff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	// A guest lists types and items only while guests may choose them (#711).
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestsCanChooseTypeItem, true))

	cat, err := h.categorySvc.CreateCategoryWithDescription(ctx, "Retired", "Category text", 90)
	require.NoError(t, err)
	tp, err := h.categorySvc.CreateTypeWithDescription(ctx, cat.ID, "Active type", "Type text", 1)
	require.NoError(t, err)
	_, err = h.categorySvc.CreateItemWithDescription(ctx, tp.ID, "Active item", "Item text", 1)
	require.NoError(t, err)

	typesPath := "/api/v1/categories/" + cat.ID.String() + "/types"
	itemsPath := typesPath + "/" + tp.ID.String() + "/items"
	count := func(res *http.Response) int {
		t.Helper()
		var rows []described
		decode(t, res, &rows)
		return len(rows)
	}
	asGuest := func(p string) *http.Response { return h.doUnauth(t, http.MethodGet, p, nil) }
	asUser := func(p string) *http.Response { return h.doAsUser(t, http.MethodGet, p, nil) }
	asStaff := func(p string) *http.Response { return h.do(t, http.MethodGet, p, nil) }
	everyone := map[string]func(string) *http.Response{"guest": asGuest, "user": asUser, "staff": asStaff}

	// Both parents active: everyone sees the children.
	for who, as := range everyone {
		require.Equal(t, 1, count(as(typesPath)), who)
		require.Equal(t, 1, count(as(itemsPath)), who)
	}

	// The type is archived, its category is not: its item is not offered.
	tp.Active = false
	require.NoError(t, h.categorySvc.UpdateType(ctx, tp))
	require.Equal(t, 0, count(asGuest(itemsPath)))
	require.Equal(t, 0, count(asUser(itemsPath)))
	require.Equal(t, 1, count(asStaff(itemsPath)))
	tp.Active = true
	require.NoError(t, h.categorySvc.UpdateType(ctx, tp))

	// The category is archived: neither its types nor their items are offered.
	cat.Active = false
	require.NoError(t, h.categorySvc.UpdateCategory(ctx, cat))
	for _, who := range []string{"guest", "user"} {
		require.Equal(t, 0, count(everyone[who](typesPath)), who)
		require.Equal(t, 0, count(everyone[who](itemsPath)), who)
	}
	require.Equal(t, 1, count(asStaff(typesPath)), "staff still see an archived category's types")
	require.Equal(t, 1, count(asStaff(itemsPath)))
}
