package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// A field's own limit on a value (owner, 2026-11-11: "fixed caps but admin can
// lower it when they're defining the field"), set on the field definition,
// enforced by the API at every door, and told to the forms.

func (h *harness) createField(t *testing.T, body map[string]any) (int, []byte) {
	t.Helper()
	return h.cf(t, http.MethodPost, cfAdmin, body)
}

func TestAdminFieldDefs_MaxLength(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for name, tc := range map[string]struct {
		body map[string]any
		want int
	}{
		"text, 100":            {map[string]any{"name": "A", "field_type": "text", "max_length": 100}, http.StatusCreated},
		"text, 254":            {map[string]any{"name": "B", "field_type": "text", "max_length": 254}, http.StatusCreated},
		"text, 255 is the cap": {map[string]any{"name": "C", "field_type": "text", "max_length": 255}, http.StatusBadRequest},
		"text, 300":            {map[string]any{"name": "D", "field_type": "text", "max_length": 300}, http.StatusBadRequest},
		"text, zero":           {map[string]any{"name": "E", "field_type": "text", "max_length": 0}, http.StatusBadRequest},
		"text, negative":       {map[string]any{"name": "F", "field_type": "text", "max_length": -1}, http.StatusBadRequest},
		"text, not a number":   {map[string]any{"name": "G", "field_type": "text", "max_length": "100"}, http.StatusBadRequest},
		"textarea, 5000":       {map[string]any{"name": "H", "field_type": "textarea", "max_length": 5000}, http.StatusCreated},
		"textarea, 10000":      {map[string]any{"name": "I", "field_type": "textarea", "max_length": 10000}, http.StatusBadRequest},
		"select":               {map[string]any{"name": "J", "field_type": "select", "options": []string{"a"}, "max_length": 10}, http.StatusBadRequest},
		"number":               {map[string]any{"name": "K", "field_type": "number", "max_length": 10}, http.StatusBadRequest},
		"null is no limit":     {map[string]any{"name": "L", "field_type": "text", "max_length": nil}, http.StatusCreated},
		"absent is no limit":   {map[string]any{"name": "M", "field_type": "text"}, http.StatusCreated},
	} {
		code, body := h.createField(t, tc.body)
		require.Equal(t, tc.want, code, "%s: %s", name, body)
	}

	code, body := h.createField(t, map[string]any{"name": "Shown", "field_type": "text", "max_length": 100})
	require.Equal(t, http.StatusCreated, code, string(body))
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.EqualValues(t, 100, got["max_length"])
	require.EqualValues(t, 100, got["effective_max_length"])

	code, body = h.createField(t, map[string]any{"name": "Default", "field_type": "textarea"})
	require.Equal(t, http.StatusCreated, code, string(body))
	got = nil
	require.NoError(t, json.Unmarshal(body, &got))
	require.NotContains(t, got, "max_length")
	require.EqualValues(t, 10_000, got["effective_max_length"], "what applies, so a form can set maxlength")
}

func TestAdminFieldDefs_PatchMaxLength(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.mkField(t, "Note", customfield.FieldTypeText, nil, 1)

	code, body := h.patchField(t, def.ID, map[string]any{"max_length": 100})
	require.Equal(t, http.StatusOK, code, string(body))
	require.Equal(t, 100, *h.storedDef(t, def.ID).MaxLength)

	code, body = h.patchField(t, def.ID, map[string]any{"name": "Renamed"})
	require.Equal(t, http.StatusOK, code, string(body))
	require.Equal(t, 100, *h.storedDef(t, def.ID).MaxLength, "not mentioned, not changed")

	for _, bad := range []any{255, 1000, 0, -3, "x"} {
		code, body = h.patchField(t, def.ID, map[string]any{"max_length": bad})
		require.Equal(t, http.StatusBadRequest, code, "%v: %s", bad, body)
	}
	require.Equal(t, 100, *h.storedDef(t, def.ID).MaxLength, "a refused change writes nothing")

	code, body = h.patchField(t, def.ID, map[string]any{"max_length": nil})
	require.Equal(t, http.StatusOK, code, "null clears it: %s", body)
	require.Nil(t, h.storedDef(t, def.ID).MaxLength)
}

// Lowering a limit below what tickets hold is allowed, and changes nothing they
// hold; a value that does not change is not checked again.
func TestAdminFieldDefs_LoweringALimitLeavesStoredValues(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.mkField(t, "Note", customfield.FieldTypeText, nil, 1)
	h.attach(t, def, customfield.CategoryScope, h.catID, 1)
	long := ascii(200)
	tk := h.holds(t, def, long)

	code, body := h.patchField(t, def.ID, map[string]any{"max_length": 50})
	require.Equal(t, http.StatusOK, code, string(body))
	require.Equal(t, long, h.storedValues(t, tk.ID.String())[def.ID.String()], "the value stays")

	path := "/api/v1/tickets/" + tk.ID.String() + "/custom-fields"
	res := h.do(t, http.MethodPut, path, map[string]string{def.ID.String(): long})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "the same value again is not checked again: %s", readAll(t, res))
	res = h.do(t, http.MethodPut, path, map[string]string{def.ID.String(): ascii(51)})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "a different one is held to the new limit")
	res.Body.Close()
	res = h.do(t, http.MethodPut, path, map[string]string{def.ID.String(): ascii(50)})
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestFieldsOwnLimit_AtEveryDoor(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, setGuests(h))
	short, err := h.customFieldSvc.CreateFieldDefLimited(ctx, "Short", customfield.FieldTypeText, nil, 1, intPtr(100), nil)
	require.NoError(t, err)
	_, err = h.customFieldSvc.Attach(ctx, customfield.Assignment{
		FieldDefID: short.ID, ScopeType: customfield.CategoryScope, ScopeID: h.catID, VisibleOnNew: true, VisibleToGuests: true,
	}, nil)
	require.NoError(t, err)
	key := short.ID.String()

	for _, caller := range []string{"staff", "admin", "user"} {
		code, body := h.postTicket(t, caller, map[string]string{key: ascii(101)})
		require.Equal(t, http.StatusBadRequest, code, "%s: %s", caller, body)
		require.Contains(t, string(body), "100", caller)
		code, body = h.postTicket(t, caller, map[string]string{key: ascii(100)})
		require.Equal(t, http.StatusCreated, code, "%s: %s", caller, body)
	}

	code, body := h.guestCreate(t, map[string]string{key: ascii(101)})
	require.Equal(t, http.StatusBadRequest, code, string(body))
	code, body = h.guestCreate(t, map[string]string{key: ascii(100)})
	require.Equal(t, http.StatusCreated, code, string(body))

	s := mcpFieldSession(t, h)
	text, isErr := h.mcpCreate(t, s, map[string]any{key: ascii(101)})
	require.True(t, isErr, text)
	require.Contains(t, text, "100")
	text, isErr = h.mcpCreate(t, s, map[string]any{key: ascii(100)})
	require.False(t, isErr, text)

	tk := h.newTicket(t, nil, nil)
	res := h.do(t, http.MethodPut, "/api/v1/tickets/"+tk.ID.String()+"/custom-fields", map[string]string{key: ascii(101)})
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	res.Body.Close()
}

// Every list the forms read tells them what applies.
func TestFieldsOwnLimit_IsToldToTheForms(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, setGuests(h))
	short, err := h.customFieldSvc.CreateFieldDefLimited(ctx, "Short", customfield.FieldTypeText, nil, 1, intPtr(100), nil)
	require.NoError(t, err)
	log, err := h.customFieldSvc.CreateFieldDef(ctx, "Log", customfield.FieldTypeTextarea, nil, 2, nil)
	require.NoError(t, err)
	for _, d := range []customfield.FieldDef{short, log} {
		_, err = h.customFieldSvc.Attach(ctx, customfield.Assignment{
			FieldDefID: d.ID, ScopeType: customfield.CategoryScope, ScopeID: h.catID, VisibleOnNew: true, VisibleToGuests: true,
		}, nil)
		require.NoError(t, err)
	}

	t.Run("GET /tickets/fields", func(t *testing.T) {
		res := h.do(t, http.MethodGet, "/api/v1/tickets/fields?category_id="+h.catID.String(), nil)
		var got []struct {
			FieldDef struct {
				Name string `json:"name"`
				Eff  int    `json:"effective_max_length"`
			} `json:"field_def"`
		}
		require.NoError(t, json.Unmarshal(readAll(t, res), &got))
		by := map[string]int{}
		for _, g := range got {
			by[g.FieldDef.Name] = g.FieldDef.Eff
		}
		require.Equal(t, map[string]int{"Short": 100, "Log": 10_000}, by)
	})
	t.Run("GET /tickets/{id}/custom-fields", func(t *testing.T) {
		tk := h.newTicket(t, nil, nil)
		require.NoError(t, h.customFieldSvc.SetValues(ctx, tk.ID, map[uuid.UUID]string{short.ID: "x"}))
		res := h.do(t, http.MethodGet, "/api/v1/tickets/"+tk.ID.String()+"/custom-fields", nil)
		var got []map[string]any
		require.NoError(t, json.Unmarshal(readAll(t, res), &got))
		require.Len(t, got, 1)
		require.EqualValues(t, 100, got[0]["effective_max_length"])
	})
	t.Run("GET /guest/fields", func(t *testing.T) {
		code, body := h.guestFields(t, h.catID.String())
		require.Equal(t, http.StatusOK, code, string(body))
		var got []map[string]any
		require.NoError(t, json.Unmarshal(body, &got))
		by := map[string]any{}
		for _, g := range got {
			by[g["name"].(string)] = g["max_length"]
		}
		require.EqualValues(t, 100, by["Short"])
		require.EqualValues(t, 10_000, by["Log"])
	})
	t.Run("MCP list_ticket_fields", func(t *testing.T) {
		s := mcpFieldSession(t, h)
		text, isErr := s.tool("list_ticket_fields", map[string]any{"category_id": h.catID.String()})
		require.False(t, isErr, text)
		var got []map[string]any
		require.NoError(t, json.Unmarshal([]byte(text), &got))
		by := map[string]any{}
		for _, g := range got {
			by[g["name"].(string)] = g["max_length"]
		}
		require.EqualValues(t, 100, by["Short"])
		require.EqualValues(t, 10_000, by["Log"])
	})
}

func TestAdminFieldDefs_MaxLengthChangeIsAudited(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.mkField(t, "Note", customfield.FieldTypeText, nil, 1)
	code, body := h.patchField(t, def.ID, map[string]any{"max_length": 100})
	require.Equal(t, http.StatusOK, code, string(body))

	var saw bool
	for _, e := range h.fieldHistory(t, def.ID) {
		if e.Action == "custom_field_max_length_changed" {
			saw = true
			require.EqualValues(t, 255, e.Before["max_length"])
			require.EqualValues(t, 100, e.After["max_length"])
			require.NotNil(t, e.ActorID)
		}
	}
	require.True(t, saw)
}

func intPtr(n int) *int { return &n }

func setGuests(h *harness) error {
	return h.adminSvc.SetBool(context.Background(), "guest_submission_enabled", true)
}
