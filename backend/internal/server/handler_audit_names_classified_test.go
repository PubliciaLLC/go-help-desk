package server_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A `classified` entry (1.3.0 audit D3) holds the ticket's category, type and
// item as ids. The Activity feed shows their names beside them, so a reader is
// not left with three UUIDs; one that has since been deleted keeps its id.
func TestAuditNames_ClassifiedEntryNamesCategoryTypeAndItem(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := createTicketVia(t, h, h.doAsUser)
	cat, err := h.categorySvc.CreateCategory(ctx, "Network", 90)
	require.NoError(t, err)
	typ, err := h.categorySvc.CreateType(ctx, cat.ID, "VPN", 1)
	require.NoError(t, err)
	item, err := h.categorySvc.CreateItem(ctx, typ.ID, "Certificate", 1)
	require.NoError(t, err)
	gone := uuid.NewString()
	plantEntry(t, h, id, "classified",
		map[string]any{"category_id": h.catID.String(), "type_id": nil, "item_id": nil},
		map[string]any{"category_id": cat.ID.String(), "type_id": typ.ID.String(), "item_id": item.ID.String()})
	plantEntry(t, h, id, "classified_gone",
		map[string]any{"category_id": gone}, map[string]any{"category_id": cat.ID.String()})

	for _, view := range []string{"admin", "ticket"} {
		t.Run(view, func(t *testing.T) {
			_, es := namesView(t, h, h.doAsAdmin, view, id)

			e := entryNamed(t, es, "classified")
			require.Equal(t, cat.ID.String(), e.After["category_id"], "the raw id stays")
			require.Equal(t, namesResolve{Name: "Network"}, e.Names[cat.ID.String()])
			require.Equal(t, namesResolve{Name: "VPN"}, e.Names[typ.ID.String()])
			require.Equal(t, namesResolve{Name: "Certificate"}, e.Names[item.ID.String()])
			require.NotEmpty(t, e.Names[h.catID.String()].Name, "the category it came from")

			g := entryNamed(t, es, "classified_gone")
			require.NotContains(t, g.Names, gone, "a deleted category has no name to show")
			require.Equal(t, namesResolve{Name: "Network"}, g.Names[cat.ID.String()])
		})
	}
}
