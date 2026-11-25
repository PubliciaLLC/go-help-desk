package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// decode reads a JSON body into out and closes it.
func decode(t *testing.T, res *http.Response, out any) {
	t.Helper()
	defer res.Body.Close()
	require.NoError(t, json.NewDecoder(res.Body).Decode(out))
}

type described struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Active      bool   `json:"active"`
}

// The admin CRUD takes and returns `description` at all three levels, refuses a
// description over 500 characters with a 400 that says so, and a PATCH that does
// not mention it leaves it alone while "" clears it.
func TestAdminCTI_DescriptionRoundTrip(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	const markup = "<b>x</b>\nsecond line"
	var cat, tp, it described

	t.Run("create at all three levels", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/categories", map[string]any{"name": "Hardware", "description": markup})
		require.Equal(t, http.StatusCreated, res.StatusCode)
		decode(t, res, &cat)
		require.Equal(t, markup, cat.Description, "stored verbatim: no escaping, no Markdown")

		base := "/api/v1/admin/categories/" + cat.ID + "/types"
		res = h.doAsAdmin(t, http.MethodPost, base, map[string]any{"name": "Laptop", "description": "  Type text  "})
		require.Equal(t, http.StatusCreated, res.StatusCode)
		decode(t, res, &tp)
		require.Equal(t, "Type text", tp.Description, "trimmed")

		res = h.doAsAdmin(t, http.MethodPost, base+"/"+tp.ID+"/items", map[string]any{"name": "Dock", "description": "Item text"})
		require.Equal(t, http.StatusCreated, res.StatusCode)
		decode(t, res, &it)
		require.Equal(t, "Item text", it.Description)
	})

	catPath := "/api/v1/admin/categories/" + cat.ID
	typePath := catPath + "/types/" + tp.ID
	itemPath := typePath + "/items/" + it.ID
	paths := map[string]string{"category": catPath, "type": typePath, "item": itemPath}

	t.Run("get and list carry it", func(t *testing.T) {
		var got described
		decode(t, h.doAsAdmin(t, http.MethodGet, catPath, nil), &got)
		require.Equal(t, markup, got.Description)
		var cats []described
		decode(t, h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/categories", nil), &cats)
		var found bool
		for _, c := range cats {
			if c.ID == cat.ID {
				found = true
				require.Equal(t, markup, c.Description)
			}
		}
		require.True(t, found)
		var types, items []described
		decode(t, h.doAsAdmin(t, http.MethodGet, catPath+"/types", nil), &types)
		require.Equal(t, "Type text", types[0].Description)
		decode(t, h.doAsAdmin(t, http.MethodGet, typePath+"/items", nil), &items)
		require.Equal(t, "Item text", items[0].Description)
	})

	t.Run("a PATCH that does not mention it leaves it alone", func(t *testing.T) {
		for level, path := range paths {
			var got described
			res := h.doAsAdmin(t, http.MethodPatch, path, map[string]any{"sort_order": 7})
			require.Equal(t, http.StatusOK, res.StatusCode, level)
			decode(t, res, &got)
			require.NotEmpty(t, got.Description, level)
		}
	})

	t.Run("a description edit that is the only change, and archiving, keep the rest", func(t *testing.T) {
		var got described
		res := h.doAsAdmin(t, http.MethodPatch, catPath, map[string]any{"description": "New text"})
		require.Equal(t, http.StatusOK, res.StatusCode)
		decode(t, res, &got)
		require.Equal(t, "New text", got.Description)
		require.Equal(t, "Hardware", got.Name)
		require.True(t, got.Active)

		res = h.doAsAdmin(t, http.MethodPatch, catPath, map[string]any{"active": false})
		decode(t, res, &got)
		require.False(t, got.Active)
		require.Equal(t, "New text", got.Description, "an archived category keeps its description")
	})

	t.Run("an empty string clears it", func(t *testing.T) {
		for level, path := range paths {
			var got described
			res := h.doAsAdmin(t, http.MethodPatch, path, map[string]any{"description": "   "})
			require.Equal(t, http.StatusOK, res.StatusCode, level)
			decode(t, res, &got)
			require.Equal(t, "", got.Description, level)
		}
	})

	t.Run("501 characters is a 400 that says why, on create and on edit", func(t *testing.T) {
		tooLong := strings.Repeat("é", 501)
		ok500 := strings.Repeat("é", 500)
		for level, path := range paths {
			res := h.doAsAdmin(t, http.MethodPatch, path, map[string]any{"description": tooLong})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, level)
			var e struct {
				Error struct{ Code, Message string }
			}
			decode(t, res, &e)
			require.Equal(t, "bad_request", e.Error.Code, level)
			require.Contains(t, e.Error.Message, "description is too long", level)
			require.Contains(t, e.Error.Message, "501", level)

			res = h.doAsAdmin(t, http.MethodPatch, path, map[string]any{"description": ok500})
			require.Equal(t, http.StatusOK, res.StatusCode, "500 characters (1000 bytes) is within the limit: "+level)
		}
		res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/categories", map[string]any{"name": "Long", "description": tooLong})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		res.Body.Close()
		res = h.doAsAdmin(t, http.MethodPost, catPath+"/types", map[string]any{"name": "Long", "description": tooLong})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		res.Body.Close()
		res = h.doAsAdmin(t, http.MethodPost, typePath+"/items", map[string]any{"name": "Long", "description": tooLong})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		res.Body.Close()

		var cats []described
		decode(t, h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/categories", nil), &cats)
		for _, c := range cats {
			require.NotEqual(t, "Long", c.Name, "a refused create must not leave a row behind")
		}
	})
}

// The lists the ticket forms read carry `description`: for staff, for a
// reporting user, and for a guest with no session at all (while guest
// submission is on, the public catalogue is public, and so is the text).
func TestCatalogue_ListsCarryDescription(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))

	cat, err := h.categorySvc.CreateCategoryWithDescription(ctx, "Hardware", "Category text", 90)
	require.NoError(t, err)
	tp, err := h.categorySvc.CreateTypeWithDescription(ctx, cat.ID, "Laptop", "Type text", 1)
	require.NoError(t, err)
	_, err = h.categorySvc.CreateItemWithDescription(ctx, tp.ID, "Dock", "Item text", 1)
	require.NoError(t, err)

	typesPath := "/api/v1/categories/" + cat.ID.String() + "/types"
	itemsPath := typesPath + "/" + tp.ID.String() + "/items"

	read := func(t *testing.T, do func(string) *http.Response) {
		t.Helper()
		var cats, types, items []described
		res := do("/api/v1/categories")
		require.Equal(t, http.StatusOK, res.StatusCode)
		decode(t, res, &cats)
		var got string
		for _, c := range cats {
			if c.ID == cat.ID.String() {
				got = c.Description
			}
		}
		require.Equal(t, "Category text", got)
		decode(t, do(typesPath), &types)
		require.Equal(t, "Type text", types[0].Description)
		decode(t, do(itemsPath), &items)
		require.Equal(t, "Item text", items[0].Description)
	}

	t.Run("staff", func(t *testing.T) {
		read(t, func(p string) *http.Response { return h.do(t, http.MethodGet, p, nil) })
	})
	t.Run("a reporting user", func(t *testing.T) {
		read(t, func(p string) *http.Response { return h.doAsUser(t, http.MethodGet, p, nil) })
	})
	t.Run("a guest with no session", func(t *testing.T) {
		read(t, func(p string) *http.Response { return h.doUnauth(t, http.MethodGet, p, nil) })
	})
}

// list_categories returns the tree with each node's description.
func TestMCP_ListCategoriesCarriesDescription(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	cat, err := h.categorySvc.CreateCategoryWithDescription(ctx, "Hardware", "Category text", 90)
	require.NoError(t, err)
	tp, err := h.categorySvc.CreateTypeWithDescription(ctx, cat.ID, "Laptop", "Type text", 1)
	require.NoError(t, err)
	_, err = h.categorySvc.CreateItemWithDescription(ctx, tp.ID, "Dock", "Item text", 1)
	require.NoError(t, err)

	c := openMCP(t, h, h.userKey)
	defer c.closeBody()
	got := c.call(t, "list_categories", map[string]any{})
	for _, want := range []string{`description\":\"Category text`, `description\":\"Type text`, `description\":\"Item text`} {
		require.Contains(t, got, want)
	}
}
