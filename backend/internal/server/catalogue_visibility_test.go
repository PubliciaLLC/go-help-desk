package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Who sees an archived category, type or item.
//
// DESIGN.md's "Ticket Submission by Role" table: Guest and User get active
// only; Staff and Admin get All. Staff need the archived ones because filing
// or reclassifying an old ticket under the classification it actually belongs
// to is ordinary work, and that classification may well have been retired.
//
// The defect this pins: the new-ticket form asked /admin/categories for staff,
// which is wrapped in RequireRole(admin). Staff got 403, the query failed, the
// picker fell back to an empty list — and since a category is required, a
// staff member could not file a ticket at all. Proven at the time:
//
//	staff  GET /admin/categories -> 403 insufficient permissions
//	admin  GET /admin/categories -> 200 [{"name":"General",...}]
//	CATEGORY OPTIONS SEEN BY STAFF: ["Select…"]
func TestCatalogue_ArchivedRowsAreStaffOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// An archived category with an archived type and item beneath it.
	retired, err := h.categorySvc.CreateCategory(ctx, "Retired Kit", 90)
	require.NoError(t, err)
	retiredType, err := h.categorySvc.CreateType(ctx, retired.ID, "Old Printer", 1)
	require.NoError(t, err)
	retiredItem, err := h.categorySvc.CreateItem(ctx, retiredType.ID, "Drum Unit", 1)
	require.NoError(t, err)

	retired.Active = false
	require.NoError(t, h.categorySvc.UpdateCategory(ctx, retired))
	retiredType.Active = false
	require.NoError(t, h.categorySvc.UpdateType(ctx, retiredType))
	retiredItem.Active = false
	require.NoError(t, h.categorySvc.UpdateItem(ctx, retiredItem))

	names := func(t *testing.T, res *http.Response) []string {
		t.Helper()
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		var rows []struct {
			Name string `json:"name"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&rows))
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.Name
		}
		return out
	}

	typesPath := "/api/v1/categories/" + retired.ID.String() + "/types"
	itemsPath := typesPath + "/" + retiredType.ID.String() + "/items"

	t.Run("staff see the whole tree", func(t *testing.T) {
		require.Contains(t, names(t, h.do(t, http.MethodGet, "/api/v1/categories", nil)),
			"Retired Kit", "a staff member cannot see the archived category, so they cannot file under it")
		require.Contains(t, names(t, h.do(t, http.MethodGet, typesPath, nil)), "Old Printer")
		require.Contains(t, names(t, h.do(t, http.MethodGet, itemsPath, nil)), "Drum Unit")
	})

	t.Run("administrators too", func(t *testing.T) {
		require.Contains(t, names(t, h.doAsAdmin(t, http.MethodGet, "/api/v1/categories", nil)),
			"Retired Kit")
	})

	t.Run("a reporting user sees only what is still in circulation", func(t *testing.T) {
		require.NotContains(t, names(t, h.doAsUser(t, http.MethodGet, "/api/v1/categories", nil)),
			"Retired Kit", "a reporter was offered a category an administrator had retired")
		require.NotContains(t, names(t, h.doAsUser(t, http.MethodGet, typesPath, nil)), "Old Printer")
	})

	// And the active ones still reach everybody, or this would "fix" the
	// staff form by breaking the reporter's.
	t.Run("everyone still sees the active tree", func(t *testing.T) {
		require.Contains(t, names(t, h.doAsUser(t, http.MethodGet, "/api/v1/categories", nil)), "General")
		require.Contains(t, names(t, h.do(t, http.MethodGet, "/api/v1/categories", nil)), "General")
	})
}
