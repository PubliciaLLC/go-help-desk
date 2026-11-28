package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// #626: the rules the admin page and the ticket form held and the API did not.

// requireField attaches a shown, required field to the harness category.
func (h *harness) requireField(t *testing.T, name string, ft customfield.FieldType, opts []string) customfield.FieldDef {
	t.Helper()
	d := h.mkField(t, name, ft, opts, 1)
	_, err := h.customFieldSvc.Attach(context.Background(), customfield.Assignment{
		FieldDefID: d.ID, ScopeType: customfield.CategoryScope, ScopeID: h.catID, VisibleOnNew: true, RequiredOnNew: true,
	}, nil)
	require.NoError(t, err)
	return d
}

// seedAttach is attach straight into the database, which is the only way left to
// make an overlap (#626: the API refuses one). For the tests about what is done
// with an overlap a 1.3.0-beta database already holds.
func (h *harness) seedAttach(t *testing.T, def customfield.FieldDef, scope customfield.ScopeType, id uuid.UUID, order int) {
	t.Helper()
	_, err := h.q.CreateCustomFieldAssignment(context.Background(), dbgen.CreateCustomFieldAssignmentParams{
		ID: uuid.New(), FieldDefID: def.ID, ScopeType: string(scope), ScopeID: id, SortOrder: int32(order), VisibleOnNew: true,
	})
	require.NoError(t, err)
}

func (h *harness) postTicket(t *testing.T, as string, fields map[string]string) (int, []byte) {
	t.Helper()
	extra := map[string]any{}
	if fields != nil {
		extra["custom_fields"] = fields
	}
	res := h.postOnBehalf(t, as, extra)
	return res.StatusCode, readAll(t, res)
}

// (a) A ticket missing a required field is a 400 for every caller, and nothing
// is written: no ticket, and no tracking number spent.
func TestCreateTicket_RequiredFieldsAreEnforced(t *testing.T) {
	for _, caller := range []string{"staff", "admin", "user"} {
		t.Run(caller, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			serial := h.requireField(t, "Serial", customfield.FieldTypeText, nil)
			optional := h.mkField(t, "Notes", customfield.FieldTypeText, nil, 2)
			h.attach(t, optional, customfield.CategoryScope, h.catID, 2)
			before := ticketCount(t, h)

			for name, fields := range map[string]map[string]string{
				"no custom_fields at all": nil,
				"an empty map":            {},
				"only the optional one":   {optional.ID.String(): "x"},
				"an empty value":          {serial.ID.String(): ""},
				"spaces":                  {serial.ID.String(): "   "},
			} {
				code, body := h.postTicket(t, caller, fields)
				require.Equal(t, http.StatusBadRequest, code, "%s: %s", name, body)
				require.Equal(t, "bad_request", errCode(t, body), name)
				require.Contains(t, string(body), "Serial", name)
			}
			require.Equal(t, before, ticketCount(t, h), "a refused request writes no ticket")

			code, body := h.postTicket(t, caller, map[string]string{serial.ID.String(): "SN-1"})
			require.Equal(t, http.StatusCreated, code, string(body))
			require.Equal(t, before+1, ticketCount(t, h))
		})
	}
}

// Staff filing for somebody else are held to it too (D25).
func TestCreateTicket_RequiredFieldsAreEnforcedOnBehalf(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	serial := h.requireField(t, "Serial", customfield.FieldTypeText, nil)
	extra := map[string]any{"reporter_user_id": h.userID.String()}

	res := h.postOnBehalf(t, "staff", extra)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, string(readAll(t, res)))

	extra["custom_fields"] = map[string]string{serial.ID.String(): "SN-1"}
	res = h.postOnBehalf(t, "staff", extra)
	require.Equal(t, http.StatusCreated, res.StatusCode, string(readAll(t, res)))
}

// What the form does not hold a person to, the API does not either: a field that
// is not shown, is deactivated, or is required at a level the ticket does not
// have.
func TestCreateTicket_RequiredOnlyWhereTheFormRequiresIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	hidden := h.mkField(t, "Hidden", customfield.FieldTypeText, nil, 1)
	_, err := h.customFieldSvc.Attach(ctx, customfield.Assignment{
		FieldDefID: hidden.ID, ScopeType: customfield.CategoryScope, ScopeID: h.catID, VisibleOnNew: false, RequiredOnNew: true,
	}, nil)
	require.NoError(t, err)
	off := h.requireField(t, "Off", customfield.FieldTypeText, nil)
	off.Active = false
	require.NoError(t, h.customFieldSvc.UpdateFieldDef(ctx, off, nil))
	typ, err := h.categorySvc.CreateType(ctx, h.catID, "Hardware", 1)
	require.NoError(t, err)
	typed := h.mkField(t, "Typed", customfield.FieldTypeText, nil, 3)
	_, err = h.customFieldSvc.Attach(ctx, customfield.Assignment{
		FieldDefID: typed.ID, ScopeType: customfield.TypeScope, ScopeID: typ.ID, VisibleOnNew: true, RequiredOnNew: true,
	}, nil)
	require.NoError(t, err)

	code, body := h.postTicket(t, "staff", nil)
	require.Equal(t, http.StatusCreated, code, "none of the three is required of a ticket without the type: %s", body)

	res := h.postOnBehalf(t, "staff", map[string]any{"type_id": typ.ID.String()})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "with the type, its required field is")
	require.Contains(t, string(readAll(t, res)), "Typed")
}

// A required select whose value would be dropped is refused, not accepted with
// a warning and a ticket that lacks the field.
func TestCreateTicket_RequiredValueMustBeAcceptable(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	pick := h.requireField(t, "Colour", customfield.FieldTypeSelect, []string{"red", "blue"})
	before := ticketCount(t, h)

	code, body := h.postTicket(t, "staff", map[string]string{pick.ID.String(): "purple"})
	require.Equal(t, http.StatusBadRequest, code, string(body))
	require.Equal(t, before, ticketCount(t, h))
}

// (b) Options.
func TestFieldDefs_RefuseBlankAndDuplicateOptions(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for name, opts := range map[string][]string{
		"blank": {"a", ""}, "spaces": {"a", "  "}, "repeated": {"a", "b", "a"},
	} {
		code, body := h.cf(t, http.MethodPost, cfAdmin, map[string]any{"name": "Pick " + name, "field_type": "select", "options": opts})
		require.Equal(t, http.StatusBadRequest, code, "create, %s: %s", name, body)
	}

	def := h.mkField(t, "Pick", customfield.FieldTypeSelect, []string{"a", "b"}, 1)
	for name, patch := range map[string]map[string]any{
		"blank":            {"options": []string{"a", "b", ""}},
		"repeated":         {"options": []string{"a", "b", "b"}},
		"blank retired":    {"options": []string{"a"}, "retired_options": []string{"b", " "}},
		"repeated retired": {"options": []string{"a"}, "retired_options": []string{"b", "b"}},
	} {
		code, body := h.patchField(t, def.ID, patch)
		require.Equal(t, http.StatusBadRequest, code, "patch, %s: %s", name, body)
	}
	require.Equal(t, []string{"a", "b"}, h.storedDef(t, def.ID).Options, "a refused patch saved nothing")

	code, body := h.patchField(t, def.ID, map[string]any{"options": []string{"a", "b", "c"}})
	require.Equal(t, http.StatusOK, code, string(body))
}

// A field already saved with a blank option can still be renamed.
func TestFieldDefs_AlreadyBadOptionsDoNotBlockARename(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.mkField(t, "Pick", customfield.FieldTypeSelect, []string{"a"}, 1)
	_, err := h.tx.ExecContext(context.Background(), `UPDATE custom_field_defs SET options = '["a","","a"]' WHERE id = $1`, def.ID)
	require.NoError(t, err)

	code, body := h.patchField(t, def.ID, map[string]any{"name": "Renamed"})
	require.Equal(t, http.StatusOK, code, string(body))
}

// (c) Overlap.
func TestAttachOverlapping_IsRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newOverlapFixture(t, h)

	f.attach(t, h, f.catPath, true, false)
	for _, path := range []string{f.typePath, f.itemPath} {
		code, body := h.cfKey(t, http.MethodPost, path, map[string]any{"field_def_id": f.fieldID, "visible_on_new": true})
		require.Equal(t, http.StatusConflict, code, string(body))
		require.Equal(t, "conflict", errCode(t, body))
		require.Contains(t, string(body), "already attached at", "it says where")
	}
	// The same field on a path of its own is fine.
	other, err := h.categorySvc.CreateCategory(context.Background(), "Software", 2)
	require.NoError(t, err)
	code, body := h.cfKey(t, http.MethodPost, "/api/v1/admin/categories/"+other.ID.String()+"/fields",
		map[string]any{"field_def_id": f.fieldID, "visible_on_new": true})
	require.Equal(t, http.StatusCreated, code, string(body))
}

// (d) The ticket routes return retired_options, to everyone who can read the
// field (D27).
func TestTicketRoutes_ReturnRetiredOptions(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	pick := h.mkField(t, "Colour", customfield.FieldTypeSelect, []string{"red", "blue"}, 1)
	h.attach(t, pick, customfield.CategoryScope, h.catID, 1)
	tk := h.holds(t, pick, "blue")
	code, body := h.patchField(t, pick.ID, map[string]any{"options": []string{"red"}, "retired_options": []string{"blue"}})
	require.Equal(t, http.StatusOK, code, string(body))

	for _, as := range []struct {
		name string
		do   func(*testing.T, string, string, any) *http.Response
	}{{"staff", h.do}, {"requester", h.doAsUser}} {
		t.Run(as.name+" /tickets/fields", func(t *testing.T) {
			res := as.do(t, http.MethodGet, "/api/v1/tickets/fields?category_id="+h.catID.String(), nil)
			var got []struct {
				FieldDef struct {
					Options        []string `json:"options"`
					RetiredOptions []string `json:"retired_options"`
				} `json:"field_def"`
			}
			require.NoError(t, json.Unmarshal(readAll(t, res), &got))
			require.Len(t, got, 1)
			require.Equal(t, []string{"red"}, got[0].FieldDef.Options)
			require.Equal(t, []string{"blue"}, got[0].FieldDef.RetiredOptions)
		})
	}
	t.Run("staff /tickets/{id}/custom-fields", func(t *testing.T) {
		res := h.do(t, http.MethodGet, "/api/v1/tickets/"+tk.ID.String()+"/custom-fields", nil)
		var got []map[string]any
		require.NoError(t, json.Unmarshal(readAll(t, res), &got))
		require.Len(t, got, 1)
		require.Equal(t, []any{"blue"}, got[0]["retired_options"])
		require.Equal(t, "blue", got[0]["value"])
	})
	t.Run("a field with none keeps the response as it was", func(t *testing.T) {
		plain := h.mkField(t, "Serial", customfield.FieldTypeText, nil, 2)
		tk2 := h.holds(t, plain, "SN")
		res := h.do(t, http.MethodGet, "/api/v1/tickets/"+tk2.ID.String()+"/custom-fields", nil)
		require.NotContains(t, string(readAll(t, res)), "retired_options")
	})
}
