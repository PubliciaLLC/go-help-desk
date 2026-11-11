package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// #627: an anonymous visitor can be offered custom fields, and exactly the ones
// an administrator chose. The guest endpoints are an unauthenticated read and
// write into the operator's data, so most of what is tested here is what a guest
// must not be able to do or learn.

// guestFixture is the harness category with one field of every kind a guest
// might be offered, wrongly or rightly, and one the operator chose.
type guestFixture struct {
	serial     customfield.FieldDef // offered, required
	colour     customfield.FieldDef // offered, optional select, one option retired
	staffOnly  customfield.FieldDef // on the form for staff, not offered to guests
	hiddenFlag customfield.FieldDef // flagged for guests but not shown on the form
	retired    customfield.FieldDef // offered, then deactivated
	typed      customfield.FieldDef // on a type of the category
	elsewhere  customfield.FieldDef // offered, on another category
	typeID     uuid.UUID
	otherCat   uuid.UUID
}

func newGuestFixture(t *testing.T, h *harness) guestFixture {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))

	mk := func(name string, ft customfield.FieldType, opts []string, scope customfield.ScopeType, scopeID uuid.UUID, visible, required, guests bool) customfield.FieldDef {
		d := h.mkField(t, name, ft, opts, 1)
		_, err := h.customFieldSvc.Attach(ctx, customfield.Assignment{
			FieldDefID: d.ID, ScopeType: scope, ScopeID: scopeID, VisibleOnNew: visible, RequiredOnNew: required, VisibleToGuests: guests,
		}, nil)
		require.NoError(t, err)
		return d
	}
	other, err := h.categorySvc.CreateCategory(ctx, "Other", 9)
	require.NoError(t, err)
	typ, err := h.categorySvc.CreateType(ctx, h.catID, "Hardware", 1)
	require.NoError(t, err)

	f := guestFixture{typeID: typ.ID, otherCat: other.ID}
	f.serial = mk("Serial", customfield.FieldTypeText, nil, customfield.CategoryScope, h.catID, true, true, true)
	f.colour = mk("Colour", customfield.FieldTypeSelect, []string{"red", "blue"}, customfield.CategoryScope, h.catID, true, false, true)
	f.staffOnly = mk("Internal escalation code", customfield.FieldTypeSelect, []string{"P1-secret", "P2-secret"}, customfield.CategoryScope, h.catID, true, true, false)
	f.hiddenFlag = mk("Hidden but flagged", customfield.FieldTypeText, nil, customfield.CategoryScope, h.catID, false, false, true)
	f.retired = mk("Retired question", customfield.FieldTypeText, nil, customfield.CategoryScope, h.catID, true, false, true)
	f.typed = mk("Typed question", customfield.FieldTypeText, nil, customfield.TypeScope, typ.ID, true, false, false)
	f.elsewhere = mk("Elsewhere", customfield.FieldTypeText, nil, customfield.CategoryScope, other.ID, true, false, true)

	f.retired.Active = false
	require.NoError(t, h.customFieldSvc.UpdateFieldDef(ctx, f.retired, nil))
	code, body := h.patchField(t, f.colour.ID, map[string]any{"options": []string{"red"}, "retired_options": []string{"blue"}})
	require.Equal(t, http.StatusOK, code, string(body))
	return f
}

func (h *harness) guestFields(t *testing.T, category string) (int, []byte) {
	t.Helper()
	res := h.doGuest(t, http.MethodGet, "/api/v1/guest/fields?category_id="+category, "", nil)
	return res.StatusCode, readAll(t, res)
}

func (h *harness) guestCreate(t *testing.T, fields map[string]string) (int, []byte) {
	t.Helper()
	body := map[string]any{
		"subject": "Printer", "description": "jammed", "category_id": h.catID.String(),
		"guest_email": "visitor@example.test", "guest_name": "Visitor",
	}
	if fields != nil {
		body["custom_fields"] = fields
	}
	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", body)
	return res.StatusCode, readAll(t, res)
}

func TestGuestFields_ListsOnlyWhatWasOffered_InANarrowShape(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newGuestFixture(t, h)

	code, body := h.guestFields(t, h.catID.String())
	require.Equal(t, http.StatusOK, code, string(body))

	var got []map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 2, "the two fields offered to guests on this category: %s", body)
	byName := map[string]map[string]any{}
	for _, e := range got {
		byName[e["name"].(string)] = e
		// Exactly the keys the form needs: nothing about sort order, where the
		// field is attached, whether it is active, or its retired options.
		for k := range e {
			require.Contains(t, []string{"id", "name", "field_type", "options", "required", "max_length"}, k, "key %q is not for guests", k)
		}
	}
	require.Equal(t, f.serial.ID.String(), byName["Serial"]["id"])
	require.Equal(t, true, byName["Serial"]["required"])
	require.Equal(t, "text", byName["Serial"]["field_type"])
	require.Equal(t, false, byName["Colour"]["required"])
	require.Equal(t, []any{"red"}, byName["Colour"]["options"], "options in use only")

	for _, secret := range []string{
		f.staffOnly.ID.String(), "Internal escalation code", "P1-secret", "P2-secret",
		f.hiddenFlag.ID.String(), f.typed.ID.String(), f.retired.ID.String(), f.elsewhere.ID.String(),
		"blue", "retired_options", "visible_to_guests", "visible_on_new", "required_on_new",
	} {
		require.NotContains(t, string(body), secret, "a guest must not learn %q", secret)
	}
}

func TestGuestFields_Gates(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	newGuestFixture(t, h)
	ctx := context.Background()

	t.Run("a category that is not open to guests", func(t *testing.T) {
		code, _ := h.guestFields(t, uuid.NewString())
		require.Equal(t, http.StatusBadRequest, code)
	})
	t.Run("an archived category", func(t *testing.T) {
		archived, err := h.categorySvc.CreateCategory(ctx, "Archived", 8)
		require.NoError(t, err)
		err = h.categorySvc.UpdateCategory(ctx, category.Category{ID: archived.ID, Name: "Archived", SortOrder: 8, Active: false})
		require.NoError(t, err)
		code, _ := h.guestFields(t, archived.ID.String())
		require.Equal(t, http.StatusBadRequest, code)
	})
	t.Run("no category, or one that is not an id", func(t *testing.T) {
		for _, c := range []string{"", "nope"} {
			code, _ := h.guestFields(t, c)
			require.Equal(t, http.StatusBadRequest, code, c)
		}
	})
	t.Run("guest submission off answers 404, like every guest route", func(t *testing.T) {
		require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, false))
		code, body := h.guestFields(t, h.catID.String())
		require.Equal(t, http.StatusNotFound, code, string(body))
		off := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", "", nil)
		other, _ := readAllBody(off)
		require.Equal(t, other, string(body), "the same 404 as any other guest route")
	})
}

func TestGuestFields_IsThrottled(t *testing.T) {
	h, cleanup := newHarnessWithRateLimit(t, 3)
	defer cleanup()
	newGuestFixture(t, h)

	for i := 0; i < 3; i++ {
		code, body := h.guestFields(t, h.catID.String())
		require.Equal(t, http.StatusOK, code, "attempt %d: %s", i+1, body)
	}
	code, _ := h.guestFields(t, h.catID.String())
	require.Equal(t, http.StatusTooManyRequests, code, "an unauthenticated read has a budget")
}

func TestGuestCreate_StoresOfferedValues(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newGuestFixture(t, h)

	code, body := h.guestCreate(t, map[string]string{f.serial.ID.String(): "SN-42", f.colour.ID.String(): "red"})
	require.Equal(t, http.StatusCreated, code, string(body))
	var created map[string]string
	require.NoError(t, json.Unmarshal(body, &created))
	require.Equal(t, map[string]string{"tracking_number": created["tracking_number"]}, created,
		"the response is the tracking number and nothing else")

	var ticketID uuid.UUID
	require.NoError(t, h.tx.QueryRowContext(context.Background(),
		`SELECT id FROM tickets WHERE tracking_number = $1`, created["tracking_number"]).Scan(&ticketID))
	require.Equal(t, map[string]string{f.serial.ID.String(): "SN-42", f.colour.ID.String(): "red"}, h.storedValues(t, ticketID.String()))
}

// A guest cannot set a field the operator did not offer them, by any spelling of
// the request, and a refused request files nothing.
func TestGuestCreate_RefusesWhatWasNotOffered(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newGuestFixture(t, h)
	ok := map[string]string{f.serial.ID.String(): "SN-1"}
	with := func(k, v string) map[string]string {
		out := map[string]string{}
		for kk, vv := range ok {
			out[kk] = vv
		}
		out[k] = v
		return out
	}
	before := ticketCount(t, h)

	for name, fields := range map[string]map[string]string{
		"a staff-only field":                   with(f.staffOnly.ID.String(), "P1-secret"),
		"a staff-only field, upper-cased id":   with(uuidUpper(f.staffOnly.ID), "P1-secret"),
		"a staff-only field with an empty one": with(f.staffOnly.ID.String(), ""),
		"a field flagged but not on the form":  with(f.hiddenFlag.ID.String(), "x"),
		"a deactivated field":                  with(f.retired.ID.String(), "x"),
		"a type's field":                       with(f.typed.ID.String(), "x"),
		"another category's field":             with(f.elsewhere.ID.String(), "x"),
		"an id that names nothing":             with(uuid.NewString(), "x"),
		"a key that is not an id":              with("../../etc/passwd", "x"),
		"a retired option":                     with(f.colour.ID.String(), "blue"),
		"an option that does not exist":        with(f.colour.ID.String(), "purple"),
		"the same offered field twice":         with(uuidUpper(f.serial.ID), "SN-2"),
	} {
		code, body := h.guestCreate(t, fields)
		require.Equal(t, http.StatusBadRequest, code, "%s: %s", name, body)
		// The refusal does not confirm that a field it was not offered exists.
		require.NotContains(t, string(body), "Internal escalation code", name)
		require.NotContains(t, string(body), "P1-secret", name)
	}
	require.Equal(t, before, ticketCount(t, h), "nothing was filed by any of them")
}

func TestGuestCreate_RequiredIsJudgedOverWhatGuestsAreOffered(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newGuestFixture(t, h)

	for name, fields := range map[string]map[string]string{
		"no custom_fields": nil, "an empty map": {}, "an empty value": {f.serial.ID.String(): ""}, "spaces": {f.serial.ID.String(): "  "},
	} {
		code, body := h.guestCreate(t, fields)
		require.Equal(t, http.StatusBadRequest, code, "%s: %s", name, body)
		require.Contains(t, string(body), "Serial", name)
	}

	// The staff form requires "Internal escalation code"; a guest is not offered it,
	// so it does not stop them.
	code, body := h.guestCreate(t, map[string]string{f.serial.ID.String(): "SN-1"})
	require.Equal(t, http.StatusCreated, code, string(body))
}

func TestGuestCreate_WithoutAnyOfferedFieldsIsAsItWas(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))
	h.requireField(t, "Staff question", customfield.FieldTypeText, nil) // required on the form, never offered to guests

	code, body := h.guestCreate(t, nil)
	require.Equal(t, http.StatusCreated, code, string(body))
	code, body = h.guestCreate(t, map[string]string{uuid.NewString(): "x"})
	require.Equal(t, http.StatusBadRequest, code, "and a guest may not name fields when none are offered: %s", body)
}

// What a guest is shown of their own ticket afterwards has no custom fields in
// it, whatever the ticket holds.
func TestGuestTicketView_ShowsNoCustomFields(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newGuestFixture(t, h)
	tk, token := seedGuestTicket(t, h)
	require.NoError(t, h.customFieldSvc.SetValues(context.Background(), tk.ID, map[uuid.UUID]string{
		f.serial.ID: "SN-1", f.staffOnly.ID: "P1-secret",
	}))

	res := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	body := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode)
	for _, s := range []string{"SN-1", "P1-secret", "custom_field", "Internal escalation code"} {
		require.NotContains(t, string(body), s)
	}
}

// The admin API: who may offer a field to guests, and where.
func TestAdminAssignment_GuestFlag(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newOverlapFixture(t, h)
	post := func(path string, extra map[string]any) (int, []byte) {
		body := map[string]any{"field_def_id": f.fieldID, "visible_on_new": true}
		for k, v := range extra {
			body[k] = v
		}
		return h.cfKey(t, http.MethodPost, path, body)
	}

	code, body := post(f.typePath, map[string]any{"visible_to_guests": true})
	require.Equal(t, http.StatusBadRequest, code, "a type's attachment cannot be offered to guests: %s", body)
	code, body = post(f.itemPath, map[string]any{"visible_to_guests": true})
	require.Equal(t, http.StatusBadRequest, code, string(body))

	code, body = post(f.catPath, map[string]any{"visible_to_guests": true})
	require.Equal(t, http.StatusCreated, code, string(body))
	var made customfield.Assignment
	require.NoError(t, json.Unmarshal(body, &made))
	require.True(t, made.VisibleToGuests)

	t.Run("it defaults to off", func(t *testing.T) {
		code, body := h.cfKey(t, http.MethodPost, "/api/v1/admin/categories/"+h.catID.String()+"/fields",
			map[string]any{"field_def_id": h.mkField(t, "Quiet", customfield.FieldTypeText, nil, 4).ID, "visible_on_new": true})
		require.Equal(t, http.StatusCreated, code, string(body))
		var a customfield.Assignment
		require.NoError(t, json.Unmarshal(body, &a))
		require.False(t, a.VisibleToGuests)
	})

	t.Run("it can be withdrawn, and the audit says so", func(t *testing.T) {
		code, body := h.cfKey(t, http.MethodPatch, f.catPath+"/"+made.ID.String(), map[string]any{"visible_to_guests": false})
		require.Equal(t, http.StatusOK, code, string(body))
		var changed customfield.Assignment
		require.NoError(t, json.Unmarshal(body, &changed))
		require.False(t, changed.VisibleToGuests)

		var saw bool
		for _, e := range h.fieldHistory(t, f.fieldID) {
			if e.Action == "custom_field_attachment_changed" && e.Before["visible_to_guests"] == true && e.After["visible_to_guests"] == false {
				saw = true
			}
		}
		require.True(t, saw, "withdrawing a field from guests is in the field's history")
	})

	t.Run("an update cannot be used to put it on a type", func(t *testing.T) {
		code, body := h.cfKey(t, http.MethodPost, f.typePath, map[string]any{"field_def_id": h.mkField(t, "Another", customfield.FieldTypeText, nil, 5).ID, "visible_on_new": true})
		require.Equal(t, http.StatusCreated, code, string(body))
		var typed customfield.Assignment
		require.NoError(t, json.Unmarshal(body, &typed))
		code, body = h.cfKey(t, http.MethodPatch, f.typePath+"/"+typed.ID.String(), map[string]any{"visible_to_guests": true})
		require.Equal(t, http.StatusBadRequest, code, string(body))
	})
}

func uuidUpper(id uuid.UUID) string {
	b := []byte(id.String())
	for i, c := range b {
		if c >= 'a' && c <= 'f' {
			b[i] = c - 32
		}
	}
	return string(b)
}
