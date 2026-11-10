package server_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
)

// REST hands the retired half of the catalogue to staff and admin only; anyone
// else gets active entries whatever they ask for (catalogueIsFullyVisible).
// MCP's include_inactive had no role check, so a reporting user could read the
// archived structure through it.
func TestMCP_ListCategoriesIncludeInactiveIsForStaff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	retired, err := h.categorySvc.CreateCategory(ctx, "Retired Hardware", 90)
	require.NoError(t, err)
	require.NoError(t, h.categorySvc.UpdateCategory(ctx, category.Category{
		ID: retired.ID, Name: retired.Name, SortOrder: retired.SortOrder, Active: false}))

	for who, tc := range map[string]struct {
		key  string
		want bool
	}{
		"a reporting user": {h.userKey, false},
		"staff":            {h.apiKey, true},
		"an admin":         {h.adminKey, true},
	} {
		t.Run(who, func(t *testing.T) {
			c := openMCP(t, h, tc.key)
			defer c.closeBody()
			got := c.call(t, "list_categories", map[string]any{"include_inactive": true})
			require.NotContains(t, got, `"isError":true`, got)
			if tc.want {
				require.Contains(t, got, "Retired Hardware")
			} else {
				require.NotContains(t, got, "Retired Hardware", "include_inactive must not widen what a requester sees")
			}
		})
	}
}
