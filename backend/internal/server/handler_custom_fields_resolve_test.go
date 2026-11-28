package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// Bug 4 (#603): a field attached at more than one level is shown once, with
// the most specific attachment's settings. Until #626 the API accepted the
// overlap that makes it happen; it now refuses it, and an overlap a database
// already holds is seeded below.

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
	def, err := h.customFieldSvc.CreateFieldDef(ctx, "Asset tag", customfield.FieldTypeText, nil, 1, nil)
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

// seed attaches the field at a level straight into the database. The API now
// refuses a second level (#626, TestAttachOverlapping_IsRefused), but a database
// written by 1.3.0-beta can hold one, and the resolver has to keep answering
// for it.
func (f overlapFixture) seed(t *testing.T, h *harness, scope customfield.ScopeType, id uuid.UUID, visible, required bool) customfield.Assignment {
	t.Helper()
	row, err := h.q.CreateCustomFieldAssignment(context.Background(), dbgen.CreateCustomFieldAssignmentParams{
		ID: uuid.New(), FieldDefID: f.fieldID, ScopeType: string(scope), ScopeID: id, SortOrder: 1,
		VisibleOnNew: visible, RequiredOnNew: required,
	})
	require.NoError(t, err)
	return customfield.Assignment{ID: row.ID, FieldDefID: row.FieldDefID, ScopeType: scope, ScopeID: id}
}

func TestResolveFields_HTTP_OverlappedFieldComesBackOnceAtItsMostSpecificLevel(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newOverlapFixture(t, h)

	f.seed(t, h, customfield.CategoryScope, f.catID, true, false)
	typeA := f.seed(t, h, customfield.TypeScope, f.typeID, true, true)

	got := f.resolve(t, h)
	require.Len(t, got, 1, "category and type: one entry")
	require.Equal(t, typeA.ID, got[0].ID)
	require.Equal(t, customfield.TypeScope, got[0].ScopeType)
	require.True(t, got[0].RequiredOnNew, "the type's settings apply")

	itemA := f.seed(t, h, customfield.ItemScope, f.itemID, false, false)
	got = f.resolve(t, h)
	require.Len(t, got, 1, "category, type and item: still one entry")
	require.Equal(t, itemA.ID, got[0].ID)
	require.Equal(t, customfield.ItemScope, got[0].ScopeType)
	require.False(t, got[0].VisibleOnNew, "the item's settings apply, including hiding the field")
	require.False(t, got[0].RequiredOnNew)
}
