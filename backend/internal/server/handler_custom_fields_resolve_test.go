package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// Bug 4 (#603): a field attached at more than one level is shown once, with
// the most specific attachment's settings, and the API never refuses the
// overlap that makes it happen.

// overlapFixture is one field attached at the category, the type and the item
// of a ticket's path, each with different settings, through the admin API.
type overlapFixture struct {
	fieldID                     uuid.UUID
	catID, typeID, itemID       uuid.UUID
	catPath, typePath, itemPath string
}

func newOverlapFixture(t *testing.T, h *harness) overlapFixture {
	t.Helper()
	ctx := context.Background()
	typ, err := h.categorySvc.CreateType(ctx, h.catID, "Hardware", 1)
	require.NoError(t, err)
	item, err := h.categorySvc.CreateItem(ctx, typ.ID, "Laptop", 1)
	require.NoError(t, err)
	def, err := h.customFieldSvc.CreateFieldDef(ctx, "Asset tag", customfield.FieldTypeText, nil, 1)
	require.NoError(t, err)

	base := "/api/v1/admin/categories/" + h.catID.String()
	return overlapFixture{
		fieldID: def.ID, catID: h.catID, typeID: typ.ID, itemID: item.ID,
		catPath:  base + "/fields",
		typePath: base + "/types/" + typ.ID.String() + "/fields",
		itemPath: base + "/types/" + typ.ID.String() + "/items/" + item.ID.String() + "/fields",
	}
}

func (f overlapFixture) attach(t *testing.T, h *harness, path string, visible, required bool) customfield.Assignment {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodPost, path, map[string]any{
		"field_def_id": f.fieldID, "sort_order": 1, "visible_on_new": visible, "required_on_new": required,
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode, "attaching at %s", path)
	var a customfield.Assignment
	decodeJSON(t, res, &a)
	return a
}

func (f overlapFixture) resolve(t *testing.T, h *harness) []customfield.Assignment {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/v1/tickets/fields?category_id="+f.catID.String()+
		"&type_id="+f.typeID.String()+"&item_id="+f.itemID.String(), nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out []customfield.Assignment
	decodeJSON(t, res, &out)
	return out
}

// The same field on the category, the type and the item is accepted at every
// level. Show-once is a rule about what a ticket sees, not about what an
// administrator may attach.
func TestAttachOverlapping_IsNotRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newOverlapFixture(t, h)

	f.attach(t, h, f.catPath, true, false)
	f.attach(t, h, f.typePath, true, false)
	f.attach(t, h, f.itemPath, true, false)
}

func TestResolveFields_HTTP_OverlappedFieldComesBackOnceAtItsMostSpecificLevel(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newOverlapFixture(t, h)

	f.attach(t, h, f.catPath, true, false)
	typeA := f.attach(t, h, f.typePath, true, true)

	got := f.resolve(t, h)
	require.Len(t, got, 1, "category and type: one entry")
	require.Equal(t, typeA.ID, got[0].ID)
	require.Equal(t, customfield.TypeScope, got[0].ScopeType)
	require.True(t, got[0].RequiredOnNew, "the type's settings apply")

	itemA := f.attach(t, h, f.itemPath, false, false)
	got = f.resolve(t, h)
	require.Len(t, got, 1, "category, type and item: still one entry")
	require.Equal(t, itemA.ID, got[0].ID)
	require.Equal(t, customfield.ItemScope, got[0].ScopeType)
	require.False(t, got[0].VisibleOnNew, "the item's settings apply, including hiding the field")
	require.False(t, got[0].RequiredOnNew)
}
