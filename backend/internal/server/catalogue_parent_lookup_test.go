package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// The public lists check that the parent is active before serving its children.
// Only "there is no such parent" reads as an empty list (an unknown id is not told
// apart from an empty one). Any other failure of that check is a 500: it must not
// fall through to the active-only list, which would serve the children of a
// parent that may be archived.
func TestCatalogue_ParentLookupFailureIsNotServedAsActive(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	// A guest lists types and items only while guests are shown them, and a
	// requester items only while registered users are (#711).
	setChoice(t, h, guestsTypeItemKey, true)
	setChoice(t, h, usersItemKey, true)

	cat, err := h.categorySvc.CreateCategory(ctx, "Hardware", 90)
	require.NoError(t, err)
	tp, err := h.categorySvc.CreateType(ctx, cat.ID, "Laptop", 1)
	require.NoError(t, err)
	_, err = h.categorySvc.CreateItem(ctx, tp.ID, "Dock", 1)
	require.NoError(t, err)

	typesPath := "/api/v1/categories/" + cat.ID.String() + "/types"
	itemsPath := typesPath + "/" + tp.ID.String() + "/items"
	unknown := "/api/v1/categories/" + uuid.NewString() + "/types"

	// A parent that does not exist: an empty list, as before.
	for _, p := range []string{unknown, unknown + "/" + uuid.NewString() + "/items"} {
		res := h.doUnauth(t, http.MethodGet, p, nil)
		require.Equal(t, http.StatusOK, res.StatusCode, p)
		var rows []described
		decode(t, res, &rows)
		require.Empty(t, rows, p)
	}

	// The lookup itself fails: a 500 for the people who are not staff.
	h.srv.FailCategoryLookupForTest(categorystore.New(h.q))
	for who, as := range map[string]func(string) *http.Response{
		"guest": func(p string) *http.Response { return h.doUnauth(t, http.MethodGet, p, nil) },
		"user":  func(p string) *http.Response { return h.doAsUser(t, http.MethodGet, p, nil) },
	} {
		for _, p := range []string{typesPath, itemsPath} {
			res := as(p)
			require.Equal(t, http.StatusInternalServerError, res.StatusCode, who+" "+p)
			res.Body.Close()
		}
	}
}
