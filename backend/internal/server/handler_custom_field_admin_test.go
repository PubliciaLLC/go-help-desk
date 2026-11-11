package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Custom Fields A (#603), PR-5: what the server now does for the admin page.

const cfAdmin = "/api/v1/admin/custom-fields"

// cf sends a request as the signed-in administrator, which the three new routes
// require, and returns the status and body.
func (h *harness) cf(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	res := h.doAsAdminSession(t, method, path, body)
	return res.StatusCode, readAll(t, res)
}

// cfKey is cf with the administrator's API key, which the routes that predate
// the page still accept.
func (h *harness) cfKey(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	res := h.doAsAdmin(t, method, path, body)
	return res.StatusCode, readAll(t, res)
}

// cfRaw is cf with a body sent exactly as given, for the ones that are not JSON.
func (h *harness) cfRaw(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Cookie", h.session(t, asAdmin))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

func (h *harness) mkField(t *testing.T, name string, ft customfield.FieldType, opts []string, number int) customfield.FieldDef {
	t.Helper()
	d, err := h.customFieldSvc.CreateFieldDef(context.Background(), name, ft, opts, number, nil)
	require.NoError(t, err)
	return d
}

func (h *harness) attach(t *testing.T, def customfield.FieldDef, scope customfield.ScopeType, id uuid.UUID, attachmentOrder int) customfield.Assignment {
	t.Helper()
	a, err := h.customFieldSvc.CreateAssignment(context.Background(), def.ID, scope, id, attachmentOrder, true, false, nil)
	require.NoError(t, err)
	return a
}

// newTicket files a ticket at the harness category (and the given type and
// item), as the harness staff user.
func (h *harness) newTicket(t *testing.T, typeID, itemID *uuid.UUID) ticket.Ticket {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "has fields", Description: "x", CategoryID: h.catID, TypeID: typeID, ItemID: itemID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)
	return tk
}

// holds makes a ticket hold a value for the field and returns the ticket.
func (h *harness) holds(t *testing.T, def customfield.FieldDef, value string) ticket.Ticket {
	t.Helper()
	tk := h.newTicket(t, nil, nil)
	require.NoError(t, h.customFieldSvc.SetValue(context.Background(), tk.ID, def.ID, value))
	return tk
}

func names(defs []resolvedField) []string {
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.FieldDef.Name
	}
	return out
}

// resolvedField is one entry of GET /tickets/fields.
type resolvedField struct {
	ID           uuid.UUID `json:"id"`
	ScopeType    string    `json:"scope_type"`
	SortOrder    int       `json:"sort_order"`
	FieldDefID   uuid.UUID `json:"field_def_id"`
	VisibleOnNew bool      `json:"visible_on_new"`
	FieldDef     struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	} `json:"field_def"`
}

func (h *harness) resolved(t *testing.T, typeID, itemID *uuid.UUID) []resolvedField {
	t.Helper()
	path := "/api/v1/tickets/fields?category_id=" + h.catID.String()
	if typeID != nil {
		path += "&type_id=" + typeID.String()
	}
	if itemID != nil {
		path += "&item_id=" + itemID.String()
	}
	res := h.do(t, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out []resolvedField
	decodeJSON(t, res, &out)
	res.Body.Close()
	return out
}

// ── Display order ────────────────────────────────────────────────────────────

type orderFixture struct {
	typeID, itemID uuid.UUID
}

func newOrderFixture(t *testing.T, h *harness) orderFixture {
	t.Helper()
	ctx := context.Background()
	typ, err := h.categorySvc.CreateType(ctx, h.catID, "Hardware", 1)
	require.NoError(t, err)
	item, err := h.categorySvc.CreateItem(ctx, typ.ID, "Laptop", 1)
	require.NoError(t, err)
	return orderFixture{typeID: typ.ID, itemID: item.ID}
}

// Levels are kept (category, then type, then item). Within a level the order is
// the field's own number and then its name, whatever the attachments say: the
// numbers here disagree across levels, and the attachments' own numbers
// disagree with the fields'.
func TestFieldOrder_GroupedByLevelThenFieldNumberThenName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newOrderFixture(t, h)

	catB := h.mkField(t, "Cat B", customfield.FieldTypeText, nil, 5)
	catA := h.mkField(t, "Cat A", customfield.FieldTypeText, nil, 5) // ties with Cat B: the name decides
	catLow := h.mkField(t, "Cat low", customfield.FieldTypeText, nil, 1)
	typ := h.mkField(t, "Type", customfield.FieldTypeText, nil, 0)
	itemHigh := h.mkField(t, "Item high", customfield.FieldTypeText, nil, 9)
	itemLow := h.mkField(t, "Item low", customfield.FieldTypeText, nil, 2)

	// Attached in an order, and with attachment numbers, that disagree with the
	// fields' numbers.
	h.attach(t, catB, customfield.CategoryScope, h.catID, 1)
	h.attach(t, catLow, customfield.CategoryScope, h.catID, 2)
	h.attach(t, catA, customfield.CategoryScope, h.catID, 3)
	h.attach(t, typ, customfield.TypeScope, f.typeID, 1)
	h.attach(t, itemHigh, customfield.ItemScope, f.itemID, 1)
	h.attach(t, itemLow, customfield.ItemScope, f.itemID, 2)

	got := h.resolved(t, &f.typeID, &f.itemID)
	require.Equal(t, []string{"Cat low", "Cat A", "Cat B", "Type", "Item low", "Item high"}, names(got))
	// field_def.sort_order carries the real number now; it used to be 0.
	require.Equal(t, 1, got[0].FieldDef.SortOrder)
	require.Equal(t, 5, got[1].FieldDef.SortOrder)
	require.Equal(t, 9, got[5].FieldDef.SortOrder)
}

func TestFieldOrder_AllZeroIsAlphabeticalWithinEachLevel(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newOrderFixture(t, h)

	for i, n := range []string{"c", "a", "b"} {
		h.attach(t, h.mkField(t, "cat "+n, customfield.FieldTypeText, nil, 0), customfield.CategoryScope, h.catID, i+1)
	}
	for i, n := range []string{"z", "y"} {
		h.attach(t, h.mkField(t, "type "+n, customfield.FieldTypeText, nil, 0), customfield.TypeScope, f.typeID, i+1)
	}

	require.Equal(t, []string{"cat a", "cat b", "cat c", "type y", "type z"}, names(h.resolved(t, &f.typeID, nil)))
}

// A field attached at two levels is listed once, in the group of the level that
// wins, which is the most specific one.
func TestFieldOrder_MostSpecificAttachmentSetsTheLevel(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newOrderFixture(t, h)

	moved := h.mkField(t, "Moved", customfield.FieldTypeText, nil, 1) // lowest number, but wins at the item
	stays := h.mkField(t, "Stays", customfield.FieldTypeText, nil, 7)
	itemOnly := h.mkField(t, "Item only", customfield.FieldTypeText, nil, 0)
	h.attach(t, moved, customfield.CategoryScope, h.catID, 1)
	h.attach(t, stays, customfield.CategoryScope, h.catID, 2)
	h.seedAttach(t, moved, customfield.ItemScope, f.itemID, 1) // the overlap, as a 1.3.0-beta database holds it
	h.attach(t, itemOnly, customfield.ItemScope, f.itemID, 2)

	require.Equal(t, []string{"Stays", "Item only", "Moved"}, names(h.resolved(t, &f.typeID, &f.itemID)))
}

// GET /tickets/{id}/custom-fields keeps its own order, field number then name.
// The page does the grouping.
func TestFieldOrder_ValuesRouteOrderUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newOrderFixture(t, h)

	// The item-level field has the lowest number: grouped by level it would come
	// last, and here it must come first.
	item := h.mkField(t, "Item", customfield.FieldTypeText, nil, 1)
	typ := h.mkField(t, "Type", customfield.FieldTypeText, nil, 2)
	cat := h.mkField(t, "Cat", customfield.FieldTypeText, nil, 3)
	h.attach(t, item, customfield.ItemScope, f.itemID, 1)
	h.attach(t, typ, customfield.TypeScope, f.typeID, 1)
	h.attach(t, cat, customfield.CategoryScope, h.catID, 1)

	tk := h.newTicket(t, &f.typeID, &f.itemID)
	for _, d := range []customfield.FieldDef{cat, typ, item} {
		require.NoError(t, h.customFieldSvc.SetValue(context.Background(), tk.ID, d.ID, "v"))
	}
	res := h.do(t, http.MethodGet, "/api/v1/tickets/"+tk.ID.String()+"/custom-fields", nil)
	var got []customfield.TicketFieldValue
	decodeJSON(t, res, &got)
	res.Body.Close()
	require.Len(t, got, 3)
	require.Equal(t, []string{"Item", "Type", "Cat"}, []string{got[0].FieldName, got[1].FieldName, got[2].FieldName})
}

// The attachment's own number is still accepted, stored and returned; nothing
// orders by it.
func TestAttachmentSortOrder_StillStoredAndReturned(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.mkField(t, "F", customfield.FieldTypeText, nil, 1)

	path := adminAPI + "/categories/" + h.catID.String() + "/fields"
	code, body := h.cfKey(t, http.MethodPost, path, map[string]any{"field_def_id": def.ID, "sort_order": 42, "visible_on_new": true})
	require.Equal(t, http.StatusCreated, code, string(body))
	var a customfield.Assignment
	require.NoError(t, json.Unmarshal(body, &a))
	require.Equal(t, 42, a.SortOrder)

	code, body = h.cfKey(t, http.MethodPatch, path+"/"+a.ID.String(), map[string]any{"sort_order": 7})
	require.Equal(t, http.StatusOK, code, string(body))
	code, body = h.cfKey(t, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, code)
	var list []customfield.Assignment
	require.NoError(t, json.Unmarshal(body, &list))
	require.Len(t, list, 1)
	require.Equal(t, 7, list[0].SortOrder)
}

// ── Where a field appears ────────────────────────────────────────────────────

type placementJSON struct {
	ID           uuid.UUID `json:"id"`
	FieldDefID   uuid.UUID `json:"field_def_id"`
	ScopeType    string    `json:"scope_type"`
	ScopeID      uuid.UUID `json:"scope_id"`
	SortOrder    int       `json:"sort_order"`
	VisibleOnNew bool      `json:"visible_on_new"`
	Required     bool      `json:"required_on_new"`
	Category     node      `json:"category"`
	Type         *node     `json:"type"`
	Item         *node     `json:"item"`
}
type node struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Active bool      `json:"active"`
}

func TestPlacements_PathsIdsAndTreeOrder(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Two categories whose sort order disagrees with creation order; under the
	// first, two types likewise, the first with two items.
	late, err := h.categorySvc.CreateCategory(ctx, "Late", 20)
	require.NoError(t, err)
	early, err := h.categorySvc.CreateCategory(ctx, "Early", 10)
	require.NoError(t, err)
	t2, err := h.categorySvc.CreateType(ctx, early.ID, "Second type", 2)
	require.NoError(t, err)
	t1, err := h.categorySvc.CreateType(ctx, early.ID, "First type", 1)
	require.NoError(t, err)
	i2, err := h.categorySvc.CreateItem(ctx, t1.ID, "Item B", 2)
	require.NoError(t, err)
	i1, err := h.categorySvc.CreateItem(ctx, t1.ID, "Item A", 1)
	require.NoError(t, err)

	def := h.mkField(t, "Where", customfield.FieldTypeText, nil, 1)
	other := h.mkField(t, "Elsewhere", customfield.FieldTypeText, nil, 2)
	// Attached deepest first, so tree order is not creation order. Seeded under the
	// API, which no longer lets one field sit at several levels of a path (#626):
	// these are the overlaps a 1.3.0-beta database holds, and the page lists them.
	h.seedAttach(t, def, customfield.ItemScope, i2.ID, 1)
	h.seedAttach(t, def, customfield.CategoryScope, late.ID, 1)
	h.seedAttach(t, def, customfield.TypeScope, t2.ID, 1)
	h.seedAttach(t, def, customfield.ItemScope, i1.ID, 1)
	h.seedAttach(t, def, customfield.CategoryScope, early.ID, 1)
	h.seedAttach(t, def, customfield.TypeScope, t1.ID, 1)
	h.attach(t, other, customfield.CategoryScope, early.ID, 1) // another field's

	code, body := h.cf(t, http.MethodGet, cfAdmin+"/"+def.ID.String()+"/assignments", nil)
	require.Equal(t, http.StatusOK, code, string(body))
	var got []placementJSON
	require.NoError(t, json.Unmarshal(body, &got))

	type place struct{ scope, cat, typ, item string }
	var gotPlaces []place
	for _, p := range got {
		require.Equal(t, def.ID, p.FieldDefID, "only this field's attachments")
		pl := place{scope: p.ScopeType, cat: p.Category.Name}
		if p.Type != nil {
			pl.typ = p.Type.Name
		}
		if p.Item != nil {
			pl.item = p.Item.Name
		}
		gotPlaces = append(gotPlaces, pl)
	}
	require.Equal(t, []place{
		{"category", "Early", "", ""},
		{"type", "Early", "First type", ""},
		{"item", "Early", "First type", "Item A"},
		{"item", "Early", "First type", "Item B"},
		{"type", "Early", "Second type", ""},
		{"category", "Late", "", ""},
	}, gotPlaces, "category by number, the whole category first, then each type with its items")

	// Ids are the nodes', and scope_id is the node the attachment ends at.
	require.Equal(t, early.ID, got[0].Category.ID)
	require.Equal(t, early.ID, got[0].ScopeID)
	require.Nil(t, got[0].Type)
	require.Nil(t, got[0].Item)
	require.Equal(t, t1.ID, got[2].Type.ID)
	require.Equal(t, i1.ID, got[2].Item.ID)
	require.Equal(t, i1.ID, got[2].ScopeID)
	require.True(t, got[0].VisibleOnNew)

	t.Run("an archived node is marked", func(t *testing.T) {
		archived := t1
		archived.Active = false
		require.NoError(t, h.categorySvc.UpdateType(ctx, archived))
		_, body := h.cf(t, http.MethodGet, cfAdmin+"/"+def.ID.String()+"/assignments", nil)
		var again []placementJSON
		require.NoError(t, json.Unmarshal(body, &again))
		require.True(t, again[0].Category.Active)
		require.False(t, again[1].Type.Active)
		require.False(t, again[2].Type.Active, "the item's type is archived too")
		require.True(t, again[2].Item.Active)
	})
}

func TestPlacements_ErrorsAndEmpty(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.mkField(t, "Nowhere", customfield.FieldTypeText, nil, 1)

	code, body := h.cf(t, http.MethodGet, cfAdmin+"/"+def.ID.String()+"/assignments", nil)
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `[]`, string(body), "a field on no form is an empty list, not null")

	code, body = h.cf(t, http.MethodGet, cfAdmin+"/"+uuid.NewString()+"/assignments", nil)
	require.Equal(t, http.StatusNotFound, code)
	require.Equal(t, "not_found", errCode(t, body))

	code, body = h.cf(t, http.MethodGet, cfAdmin+"/not-a-uuid/assignments", nil)
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "bad_request", errCode(t, body))
}

// An attachment whose node is gone (scope_id has no foreign key) is not a place,
// and is not counted in the list's place_count either.
func TestPlacements_AnOrphanedAttachmentIsNotAPlace(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.mkField(t, "Orphaned", customfield.FieldTypeText, nil, 1)
	h.attach(t, def, customfield.CategoryScope, h.catID, 1)
	h.attach(t, def, customfield.CategoryScope, uuid.New(), 1) // names no category
	h.attach(t, def, customfield.TypeScope, uuid.New(), 1)
	h.attach(t, def, customfield.ItemScope, uuid.New(), 1)

	code, body := h.cf(t, http.MethodGet, cfAdmin+"/"+def.ID.String()+"/assignments", nil)
	require.Equal(t, http.StatusOK, code)
	var got []placementJSON
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 1)

	require.Equal(t, 1, placeCounts(t, h)[def.ID])
}

// ── place_count on the list ──────────────────────────────────────────────────

func placeCounts(t *testing.T, h *harness) map[uuid.UUID]int {
	t.Helper()
	code, body := h.cfKey(t, http.MethodGet, cfAdmin, nil)
	require.Equal(t, http.StatusOK, code, string(body))
	var raw []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &raw))
	out := map[uuid.UUID]int{}
	for _, r := range raw {
		var id uuid.UUID
		require.NoError(t, json.Unmarshal(r["id"], &id))
		pc, ok := r["place_count"]
		require.True(t, ok, "every field in the list carries place_count, 0 included")
		var n int
		require.NoError(t, json.Unmarshal(pc, &n))
		out[id] = n
	}
	return out
}

func TestListFieldDefs_PlaceCount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, tp, it := mkCTI(t, h)
	none := h.mkField(t, "None", customfield.FieldTypeText, nil, 1)
	one := h.mkField(t, "One", customfield.FieldTypeText, nil, 2)
	three := h.mkField(t, "Three", customfield.FieldTypeText, nil, 3)
	h.attach(t, one, customfield.CategoryScope, c.ID, 1)
	h.attach(t, three, customfield.CategoryScope, c.ID, 1)
	h.seedAttach(t, three, customfield.TypeScope, tp.ID, 1) // overlapping places are still places (seeded: the API refuses a new one, #626)
	h.seedAttach(t, three, customfield.ItemScope, it.ID, 1)

	got := placeCounts(t, h)
	require.Equal(t, 0, got[none.ID], "0 is present, not left out")
	require.Equal(t, 1, got[one.ID])
	require.Equal(t, 3, got[three.ID])

	// It follows the attachments, and agrees with the field's own placements.
	code, body := h.cf(t, http.MethodGet, cfAdmin+"/"+three.ID.String()+"/assignments", nil)
	require.Equal(t, http.StatusOK, code)
	var places []placementJSON
	require.NoError(t, json.Unmarshal(body, &places))
	require.Len(t, places, got[three.ID])

	asg, err := h.customFieldSvc.ListAssignments(context.Background(), customfield.ItemScope, it.ID)
	require.NoError(t, err)
	require.NoError(t, h.customFieldSvc.DeleteAssignment(context.Background(), asg[0].ID, nil))
	require.Equal(t, 2, placeCounts(t, h)[three.ID])
}

// One query for the whole list, not one per field.
func TestListFieldDefs_PlaceCountIsOneQuery(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for i := range 5 {
		d := h.mkField(t, "F"+string(rune('A'+i)), customfield.FieldTypeText, nil, i)
		h.attach(t, d, customfield.CategoryScope, h.catID, 1)
	}
	defs, err := h.customFieldSvc.ListFieldDefs(context.Background())
	require.NoError(t, err)
	require.Len(t, defs, 5)
	for _, d := range defs {
		require.NotNil(t, d.PlaceCount, "set by the list itself, with no further query to make")
		require.Equal(t, 1, *d.PlaceCount)
	}
}

// ── Reorder ──────────────────────────────────────────────────────────────────

func fieldIDs(t *testing.T, body []byte) []uuid.UUID {
	t.Helper()
	var defs []customfield.FieldDef
	require.NoError(t, json.Unmarshal(body, &defs))
	out := make([]uuid.UUID, len(defs))
	for i, d := range defs {
		out[i] = d.ID
	}
	return out
}

func TestReorder_SetsTheNumbersAndLeavesAttachmentsAlone(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	a := h.mkField(t, "A", customfield.FieldTypeText, nil, 1)
	b := h.mkField(t, "B", customfield.FieldTypeText, nil, 2)
	inactive := h.mkField(t, "C inactive", customfield.FieldTypeText, nil, 3)
	inactive.Active = false
	require.NoError(t, h.customFieldSvc.UpdateFieldDef(context.Background(), inactive, nil))
	asg := h.attach(t, a, customfield.CategoryScope, h.catID, 77)

	code, body := h.cf(t, http.MethodPut, cfAdmin+"/order", map[string]any{
		"field_def_ids": []uuid.UUID{inactive.ID, b.ID, a.ID}, // inactive fields are named too
	})
	require.Equal(t, http.StatusOK, code, string(body))
	require.Equal(t, []uuid.UUID{inactive.ID, b.ID, a.ID}, fieldIDs(t, body), "the full list, in the new order")
	var defs []customfield.FieldDef
	require.NoError(t, json.Unmarshal(body, &defs))
	require.Equal(t, []int{1, 2, 3}, []int{defs[0].SortOrder, defs[1].SortOrder, defs[2].SortOrder})
	// It answers with the list, so with each field's place_count too.
	for _, d := range defs {
		require.NotNil(t, d.PlaceCount, d.Name)
	}
	require.Equal(t, 1, *defs[2].PlaceCount, "a is attached once")
	require.Zero(t, *defs[0].PlaceCount)

	got, err := h.customFieldSvc.GetAssignment(context.Background(), asg.ID)
	require.NoError(t, err)
	require.Equal(t, 77, got.SortOrder, "attachments are not changed")
}

func TestReorder_Refusals(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	a := h.mkField(t, "A", customfield.FieldTypeText, nil, 4)
	b := h.mkField(t, "B", customfield.FieldTypeText, nil, 5)
	unchanged := func() {
		t.Helper()
		_, body := h.cfKey(t, http.MethodGet, cfAdmin, nil)
		require.Equal(t, []uuid.UUID{a.ID, b.ID}, fieldIDs(t, body))
		var defs []customfield.FieldDef
		require.NoError(t, json.Unmarshal(body, &defs))
		require.Equal(t, []int{4, 5}, []int{defs[0].SortOrder, defs[1].SortOrder}, "nothing was written")
	}

	for _, tc := range []struct {
		name string
		body string
		code int
		want string
	}{
		{"a duplicate", `{"field_def_ids":["` + a.ID.String() + `","` + a.ID.String() + `"]}`, 400, "bad_request"},
		{"a value that is not a uuid", `{"field_def_ids":["nope"]}`, 400, "bad_request"},
		{"a missing key", `{}`, 400, "bad_request"},
		{"null", `{"field_def_ids":null}`, 400, "bad_request"},
		{"invalid JSON", `{`, 400, "bad_request"},
		{"a field missing from the list", `{"field_def_ids":["` + a.ID.String() + `"]}`, 409, "conflict"},
		{"a field that does not exist", `{"field_def_ids":["` + a.ID.String() + `","` + b.ID.String() + `","` + uuid.NewString() + `"]}`, 409, "conflict"},
		{"an unknown field in place of a real one", `{"field_def_ids":["` + a.ID.String() + `","` + uuid.NewString() + `"]}`, 409, "conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := h.cfRaw(t, http.MethodPut, cfAdmin+"/order", tc.body)
			out := readAll(t, res)
			require.Equal(t, tc.code, res.StatusCode, string(out))
			require.Equal(t, tc.want, errCode(t, out))
			unchanged()
		})
	}
}

func TestReorder_NoFieldsAndAnEmptyList(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, body := h.cf(t, http.MethodPut, cfAdmin+"/order", map[string]any{"field_def_ids": []uuid.UUID{}})
	require.Equal(t, http.StatusOK, code, string(body))
	require.JSONEq(t, `[]`, string(body))
}

// ── Usage ────────────────────────────────────────────────────────────────────

func TestUsage_CountsEachValue(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sel := h.mkField(t, "Brand", customfield.FieldTypeSelect, []string{"Dell", "HP", "Acer"}, 1)
	h.holds(t, sel, "Dell")
	h.holds(t, sel, "Dell")
	h.holds(t, sel, "Acer")
	// Acer retired, and a value in neither list (set before it left the options).
	h.holds(t, sel, "HP")
	code, body := h.cfKey(t, http.MethodPatch, cfAdmin+"/"+sel.ID.String(), map[string]any{
		"options": []string{"Dell"}, "retired_options": []string{"Acer", "HP"},
	})
	require.Equal(t, http.StatusOK, code, string(body))
	tk := h.newTicket(t, nil, nil)
	_, err := h.tx.ExecContext(context.Background(), `INSERT INTO ticket_custom_field_values (ticket_id, field_def_id, value) VALUES ($1, $2, 'Zebra')`, tk.ID, sel.ID)
	require.NoError(t, err)

	code, body = h.cf(t, http.MethodGet, cfAdmin+"/"+sel.ID.String()+"/usage", nil)
	require.Equal(t, http.StatusOK, code, string(body))
	require.JSONEq(t, `{
		"tickets_with_value": 5,
		"values": [
			{"value": "Dell", "tickets": 2},
			{"value": "Acer", "tickets": 1},
			{"value": "HP", "tickets": 1},
			{"value": "Zebra", "tickets": 1}
		]}`, string(body), "in use, then retired, then in neither list")
}

func TestUsage_OtherTypesAndErrors(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	text := h.mkField(t, "Serial", customfield.FieldTypeText, nil, 1)
	h.holds(t, text, "SN-1")

	code, body := h.cf(t, http.MethodGet, cfAdmin+"/"+text.ID.String()+"/usage", nil)
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{"tickets_with_value": 1, "values": []}`, string(body), "[] and not null")

	unused := h.mkField(t, "Unused", customfield.FieldTypeSelect, []string{"a"}, 2)
	code, body = h.cf(t, http.MethodGet, cfAdmin+"/"+unused.ID.String()+"/usage", nil)
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{"tickets_with_value": 0, "values": [{"value": "a", "tickets": 0}]}`, string(body))

	code, body = h.cf(t, http.MethodGet, cfAdmin+"/"+uuid.NewString()+"/usage", nil)
	require.Equal(t, http.StatusNotFound, code)
	require.Equal(t, "not_found", errCode(t, body))
	code, body = h.cf(t, http.MethodGet, cfAdmin+"/nope/usage", nil)
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "bad_request", errCode(t, body))
}

// ── Options: delete, retire, type lock ───────────────────────────────────────

func (h *harness) patchField(t *testing.T, id uuid.UUID, body map[string]any) (int, []byte) {
	t.Helper()
	return h.cfKey(t, http.MethodPatch, cfAdmin+"/"+id.String(), body)
}

func (h *harness) storedDef(t *testing.T, id uuid.UUID) customfield.FieldDef {
	t.Helper()
	d, err := h.customFieldSvc.GetFieldDef(context.Background(), id)
	require.NoError(t, err)
	return d
}

func TestDeleteOption(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sel := h.mkField(t, "Brand", customfield.FieldTypeSelect, []string{"Dell", "HP", "Acer"}, 1)
	h.holds(t, sel, "HP")

	t.Run("an unused option is deleted", func(t *testing.T) {
		code, body := h.patchField(t, sel.ID, map[string]any{"options": []string{"Dell", "HP"}})
		require.Equal(t, http.StatusOK, code, string(body))
		require.Equal(t, []string{"Dell", "HP"}, h.storedDef(t, sel.ID).Options)
	})
	t.Run("an option a ticket holds is refused, and nothing changes", func(t *testing.T) {
		code, body := h.patchField(t, sel.ID, map[string]any{"name": "Renamed too", "options": []string{"Dell"}})
		require.Equal(t, http.StatusConflict, code, string(body))
		require.Equal(t, "conflict", errCode(t, body))
		require.Contains(t, string(body), `option \"HP\" is in use; retire it instead`)
		got := h.storedDef(t, sel.ID)
		require.Equal(t, []string{"Dell", "HP"}, got.Options)
		require.Equal(t, "Brand", got.Name, "the rest of the request is not applied either")
	})
	t.Run("moving the same option to retired_options succeeds", func(t *testing.T) {
		code, body := h.patchField(t, sel.ID, map[string]any{"options": []string{"Dell"}, "retired_options": []string{"HP"}})
		require.Equal(t, http.StatusOK, code, string(body))
		var got customfield.FieldDef
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, []string{"Dell"}, got.Options)
		require.Equal(t, []string{"HP"}, got.RetiredOptions)
	})
	t.Run("a retired option a ticket holds cannot be deleted either", func(t *testing.T) {
		code, body := h.patchField(t, sel.ID, map[string]any{"retired_options": []string{}})
		require.Equal(t, http.StatusConflict, code, string(body))
		require.Equal(t, []string{"HP"}, h.storedDef(t, sel.ID).RetiredOptions)
	})
	t.Run("bringing it back", func(t *testing.T) {
		code, body := h.patchField(t, sel.ID, map[string]any{"options": []string{"Dell", "HP"}, "retired_options": []string{}})
		require.Equal(t, http.StatusOK, code, string(body))
		got := h.storedDef(t, sel.ID)
		require.Equal(t, []string{"Dell", "HP"}, got.Options)
		require.Empty(t, got.RetiredOptions)
	})
	t.Run("a value cannot be retired without having been an option", func(t *testing.T) {
		code, body := h.patchField(t, sel.ID, map[string]any{"retired_options": []string{"Lenovo"}})
		require.Equal(t, http.StatusBadRequest, code, string(body))
	})
	t.Run("a value in both lists is refused", func(t *testing.T) {
		code, _ := h.patchField(t, sel.ID, map[string]any{"retired_options": []string{"Dell"}})
		require.Equal(t, http.StatusBadRequest, code)
	})
	t.Run("a select field keeps an option in use", func(t *testing.T) {
		code, _ := h.patchField(t, sel.ID, map[string]any{"options": []string{}, "retired_options": []string{"Dell", "HP"}})
		require.Equal(t, http.StatusBadRequest, code)
	})
	t.Run("an unknown field is 404", func(t *testing.T) {
		code, _ := h.patchField(t, uuid.New(), map[string]any{"name": "x"})
		require.Equal(t, http.StatusNotFound, code)
	})
}

func TestTypeLock(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.mkField(t, "Cost", customfield.FieldTypeText, nil, 1)

	code, body := h.patchField(t, def.ID, map[string]any{"field_type": "textarea"})
	require.Equal(t, http.StatusOK, code, "a change while the field is unused succeeds: %s", body)

	tk := h.holds(t, def, "12")
	code, body = h.patchField(t, def.ID, map[string]any{"field_type": "number"})
	require.Equal(t, http.StatusConflict, code, string(body))
	require.Equal(t, "conflict", errCode(t, body))
	require.Contains(t, string(body), "the type can't change once a ticket has a value for this field")
	require.Equal(t, customfield.FieldTypeTextarea, h.storedDef(t, def.ID).FieldType)

	// Saying the type it already has is not a change.
	code, body = h.patchField(t, def.ID, map[string]any{"field_type": "textarea", "name": "Cost 2"})
	require.Equal(t, http.StatusOK, code, string(body))

	require.NoError(t, h.customFieldSvc.SetValue(context.Background(), tk.ID, def.ID, ""))
	code, body = h.patchField(t, def.ID, map[string]any{"field_type": "number"})
	require.Equal(t, http.StatusOK, code, "unlocked again once the value is cleared: %s", body)
}

// ── Retired options over REST ────────────────────────────────────────────────

func TestRetiredOptions_OverREST(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sel := h.mkField(t, "Brand", customfield.FieldTypeSelect, []string{"Dell", "HP"}, 1)
	h.attach(t, sel, customfield.CategoryScope, h.catID, 1)
	tk := h.newTicket(t, nil, nil)
	require.NoError(t, h.customFieldSvc.SetValue(context.Background(), tk.ID, sel.ID, "HP"))
	code, body := h.patchField(t, sel.ID, map[string]any{"options": []string{"Dell"}, "retired_options": []string{"HP"}})
	require.Equal(t, http.StatusOK, code, string(body))

	t.Run("the admin list returns both lists", func(t *testing.T) {
		code, body := h.cfKey(t, http.MethodGet, cfAdmin, nil)
		require.Equal(t, http.StatusOK, code)
		var list []map[string]any
		require.NoError(t, json.Unmarshal(body, &list))
		require.Equal(t, []any{"Dell"}, list[0]["options"])
		require.Equal(t, []any{"HP"}, list[0]["retired_options"])
	})
	t.Run("a field with none retired leaves the key out", func(t *testing.T) {
		plain := h.mkField(t, "Plain", customfield.FieldTypeSelect, []string{"a"}, 2)
		code, body := h.cfKey(t, http.MethodGet, cfAdmin, nil)
		require.Equal(t, http.StatusOK, code)
		var list []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &list))
		for _, f := range list {
			if string(f["id"]) == `"`+plain.ID.String()+`"` {
				require.NotContains(t, f, "retired_options")
			}
		}
	})
	// #626(d): the ticket routes used to leave retired_options out; they return it
	// now, so a client can show a held value as retired. options is still only the
	// options in use.
	t.Run("the ticket routes offer options in use and name the retired ones", func(t *testing.T) {
		res := h.do(t, http.MethodGet, "/api/v1/tickets/fields?category_id="+h.catID.String(), nil)
		raw := readAll(t, res)
		require.Contains(t, string(raw), `"retired_options":["HP"]`)
		var fields []resolvedFieldOptions
		require.NoError(t, json.Unmarshal(raw, &fields))
		require.Equal(t, []string{"Dell"}, fields[0].FieldDef.Options)

		res = h.do(t, http.MethodGet, "/api/v1/tickets/"+tk.ID.String()+"/custom-fields", nil)
		raw = readAll(t, res)
		require.Contains(t, string(raw), `"retired_options":["HP"]`)
		var vals []customfield.TicketFieldValue
		require.NoError(t, json.Unmarshal(raw, &vals))
		require.Len(t, vals, 1, "a held retired value still comes back")
		require.Equal(t, "HP", vals[0].Value)
		require.Equal(t, []string{"HP"}, vals[0].RetiredOptions)
	})
	t.Run("a PUT with a retired value is refused", func(t *testing.T) {
		other := h.newTicket(t, nil, nil)
		res := h.do(t, http.MethodPut, "/api/v1/tickets/"+other.ID.String()+"/custom-fields", map[string]string{sel.ID.String(): "HP"})
		out := readAll(t, res)
		require.Equal(t, http.StatusBadRequest, res.StatusCode, string(out))
		require.Contains(t, string(out), "retired")
	})
	t.Run("a ticket that holds it can still save another field, and clear it", func(t *testing.T) {
		text := h.mkField(t, "Serial", customfield.FieldTypeText, nil, 3)
		h.attach(t, text, customfield.CategoryScope, h.catID, 2)
		res := h.do(t, http.MethodPut, "/api/v1/tickets/"+tk.ID.String()+"/custom-fields", map[string]string{text.ID.String(): "SN"})
		res.Body.Close()
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		res = h.do(t, http.MethodPut, "/api/v1/tickets/"+tk.ID.String()+"/custom-fields", map[string]string{sel.ID.String(): ""})
		res.Body.Close()
		require.Equal(t, http.StatusNoContent, res.StatusCode)
	})
}

type resolvedFieldOptions struct {
	FieldDef struct {
		Options []string `json:"options"`
	} `json:"field_def"`
}

// ── Who may ──────────────────────────────────────────────────────────────────

// The three calls the page added are for a signed-in administrator. A key or a
// client, however it is scoped, is refused; the routes that predate the page
// still take an administrator's key.
func TestNewCustomFieldRoutes_Auth(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.mkField(t, "Auth", customfield.FieldTypeText, nil, 1)
	client := h.newOverScopedServiceClient(t, "svc", "categories:read", "categories:write")
	// An administrator's key with exactly the scope the existing routes ask for.
	scoped := h.mintMine(t, h.session(t, asAdmin), "categories:write").Token
	withKey := func(token, method, path string, body any) *http.Response {
		var buf bytes.Buffer
		if body != nil {
			require.NoError(t, json.NewEncoder(&buf).Encode(body))
		}
		req := httptest.NewRequest(method, path, &buf)
		req.Header.Set("Authorization", "ApiKey "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.srv.ServeHTTP(rr, req)
		return rr.Result()
	}

	routes := []struct {
		name, method, path string
		body               any
	}{
		{"assignments", http.MethodGet, cfAdmin + "/" + def.ID.String() + "/assignments", nil},
		{"usage", http.MethodGet, cfAdmin + "/" + def.ID.String() + "/usage", nil},
		{"order", http.MethodPut, cfAdmin + "/order", map[string]any{"field_def_ids": []uuid.UUID{def.ID}}},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			// 403 for a caller who may not, 401 for a session that has not
			// finished signing in (it is not a session yet).
			refused := map[string]struct {
				res  *http.Response
				code int
			}{
				"staff":       {h.doSession(t, h.session(t, asStaff), rt.method, rt.path, rt.body), http.StatusForbidden},
				"a requester": {h.doSession(t, h.session(t, asReporter), rt.method, rt.path, rt.body), http.StatusForbidden},
				"an administrator who has not passed MFA": {h.doSession(t,
					h.sessionCookieFor(t, auth.SessionData{UserID: h.adminID, Role: user.RoleAdmin, MFAPassed: false}),
					rt.method, rt.path, rt.body), http.StatusUnauthorized},
				"an administrator's API key":              {h.doAsAdmin(t, rt.method, rt.path, rt.body), http.StatusForbidden},
				"an administrator's key, narrowly scoped": {withKey(scoped, rt.method, rt.path, rt.body), http.StatusForbidden},
				"an OAuth client with scopes":             {h.doBearer(t, client.Token, rt.method, rt.path, rt.body), http.StatusForbidden},
			}
			for who, r := range refused {
				out := readAll(t, r.res)
				require.Equal(t, r.code, r.res.StatusCode, "%s: %s", who, out)
			}
			// And the signed-in administrator is let in.
			res := h.doAsAdminSession(t, rt.method, rt.path, rt.body)
			out := readAll(t, res)
			require.Equal(t, http.StatusOK, res.StatusCode, string(out))
		})
	}

	t.Run("a key is refused because it is a key", func(t *testing.T) {
		res := withKey(scoped, http.MethodGet, routes[0].path, nil)
		require.Equal(t, "session_required", errCode(t, readAll(t, res)))
	})

	t.Run("the routes that predate the page still accept an administrator's key", func(t *testing.T) {
		res := withKey(scoped, http.MethodGet, cfAdmin, nil)
		require.Equal(t, http.StatusOK, res.StatusCode, string(readAll(t, res)))
		res = withKey(scoped, http.MethodPost, cfAdmin, map[string]any{"name": "Made by key", "field_type": "text"})
		require.Equal(t, http.StatusCreated, res.StatusCode, string(readAll(t, res)))
		res = withKey(scoped, http.MethodPatch, cfAdmin+"/"+def.ID.String(), map[string]any{"name": "Renamed by key"})
		require.Equal(t, http.StatusOK, res.StatusCode, string(readAll(t, res)))
		code, body := h.cfKey(t, http.MethodGet, cfAdmin, nil)
		require.Equal(t, http.StatusOK, code, string(body))
	})
}

// options has always been stored as sent for any field type, and only select
// fields use them (the create route's description says so). retired_options is
// treated the same, so a text field takes both and ignores both: nothing shows
// them on a ticket, and the rules about retiring apply to select fields only.
func TestPatch_NonSelectFieldTakesRetiredOptionsLikeOptions(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	text := h.mkField(t, "Serial", customfield.FieldTypeText, nil, 1)

	code, body := h.patchField(t, text.ID, map[string]any{"options": []string{"a"}})
	require.Equal(t, http.StatusOK, code, string(body))
	code, body = h.patchField(t, text.ID, map[string]any{"retired_options": []string{"b"}})
	require.Equal(t, http.StatusOK, code, string(body))

	got := h.storedDef(t, text.ID)
	require.Equal(t, []string{"a"}, got.Options)
	require.Equal(t, []string{"b"}, got.RetiredOptions)

	// And it changes nothing about how the field is written: any text is a value.
	tk := h.newTicket(t, nil, nil)
	require.NoError(t, h.customFieldSvc.SetValue(context.Background(), tk.ID, text.ID, "b"))
}
