package server_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// #627: MCP create_ticket takes custom fields, list_ticket_fields says which
// there are, and get_ticket reads them back. The MCP caller is an API key or an
// OAuth client; create_ticket is for staff and administrators.

func mcpFieldSession(t *testing.T, h *harness) *mcpSession {
	t.Helper()
	writer := h.newServiceClient(t, "Field agent", "tickets:read", "tickets:write")
	return newMCPSession(t, h, writer.Token)
}

func (h *harness) mcpCreate(t *testing.T, s *mcpSession, fields any) (string, bool) {
	t.Helper()
	args := map[string]any{"subject": "Raised over MCP", "description": "x", "category_id": h.catID.String()}
	if fields != nil {
		args["custom_fields"] = fields
	}
	return s.tool("create_ticket", args)
}

func TestMCPCreateTicket_RequiredFieldsAreEnforced(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := mcpFieldSession(t, h)
	serial := h.requireField(t, "Serial", customfield.FieldTypeText, nil)
	before := ticketCount(t, h)

	for name, fields := range map[string]any{
		"none": nil, "an empty object": map[string]any{}, "an empty value": map[string]any{serial.ID.String(): ""},
	} {
		text, isErr := h.mcpCreate(t, s, fields)
		require.True(t, isErr, "%s: %s", name, text)
		require.Contains(t, text, "Serial", name)
	}
	require.Equal(t, before, ticketCount(t, h), "a refused call writes nothing")

	text, isErr := h.mcpCreate(t, s, map[string]any{serial.ID.String(): "SN-1"})
	require.False(t, isErr, text)
	var created struct {
		ID       string                `json:"id"`
		Warnings []customfield.Warning `json:"warnings"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &created))
	require.Empty(t, created.Warnings)
	require.Equal(t, map[string]string{serial.ID.String(): "SN-1"}, h.storedValues(t, created.ID))
}

func TestMCPCreateTicket_ReportsDroppedValuesAsWarnings(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := mcpFieldSession(t, h)
	pick := h.mkField(t, "Colour", customfield.FieldTypeSelect, []string{"red"}, 1)
	h.attach(t, pick, customfield.CategoryScope, h.catID, 1)
	elsewhere := h.mkField(t, "Elsewhere", customfield.FieldTypeText, nil, 2)

	text, isErr := h.mcpCreate(t, s, map[string]any{pick.ID.String(): "purple", elsewhere.ID.String(): "x"})
	require.False(t, isErr, "an optional value that is dropped does not refuse the ticket: %s", text)
	var created struct {
		ID       string                `json:"id"`
		Warnings []customfield.Warning `json:"warnings"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &created))
	require.ElementsMatch(t, []customfield.Warning{
		{FieldID: pick.ID.String(), Reason: customfield.ReasonNotAnOption},
		{FieldID: elsewhere.ID.String(), Reason: customfield.ReasonNotOnTicket},
	}, created.Warnings)
	require.Empty(t, h.storedValues(t, created.ID))
}

func TestMCPCreateTicket_CustomFieldsMustBeAnObjectOfStrings(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := mcpFieldSession(t, h)
	id := h.mkField(t, "Serial", customfield.FieldTypeText, nil, 1).ID.String()
	before := ticketCount(t, h)

	for name, fields := range map[string]any{
		"a string": "SN-1", "a list": []any{"SN-1"}, "a number value": map[string]any{id: 12}, "a null value": map[string]any{id: nil},
	} {
		text, isErr := h.mcpCreate(t, s, fields)
		require.True(t, isErr, "%s: %s", name, text)
		require.Contains(t, text, "custom_fields", name)
	}
	require.Equal(t, before, ticketCount(t, h))
}

func TestMCPListTicketFields(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	s := mcpFieldSession(t, h)

	serial := h.requireField(t, "Serial", customfield.FieldTypeText, nil)
	pick := h.mkField(t, "Colour", customfield.FieldTypeSelect, []string{"red", "blue"}, 2)
	h.attach(t, pick, customfield.CategoryScope, h.catID, 2)
	code, body := h.patchField(t, pick.ID, map[string]any{"options": []string{"red"}, "retired_options": []string{"blue"}})
	require.Equal(t, 200, code, string(body))
	hidden := h.mkField(t, "Staff note", customfield.FieldTypeText, nil, 3)
	_, err := h.customFieldSvc.Attach(ctx, customfield.Assignment{FieldDefID: hidden.ID, ScopeType: customfield.CategoryScope, ScopeID: h.catID}, nil)
	require.NoError(t, err)
	gone := h.mkField(t, "Gone", customfield.FieldTypeText, nil, 4)
	h.attach(t, gone, customfield.CategoryScope, h.catID, 4)
	gone.Active = false
	require.NoError(t, h.customFieldSvc.UpdateFieldDef(ctx, gone, nil))
	typ, err := h.categorySvc.CreateType(ctx, h.catID, "Hardware", 1)
	require.NoError(t, err)
	typed := h.mkField(t, "Asset tag", customfield.FieldTypeText, nil, 5)
	h.attach(t, typed, customfield.TypeScope, typ.ID, 5)

	type entry struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		FieldType string   `json:"field_type"`
		Options   []string `json:"options"`
		Required  bool     `json:"required"`
		Level     string   `json:"level"`
	}
	list := func(args map[string]any) ([]entry, string) {
		text, isErr := s.tool("list_ticket_fields", args)
		require.False(t, isErr, text)
		var out []entry
		require.NoError(t, json.Unmarshal([]byte(text), &out))
		return out, text
	}

	got, text := list(map[string]any{"category_id": h.catID.String()})
	require.Equal(t, []entry{
		{ID: serial.ID.String(), Name: "Serial", FieldType: "text", Required: true, Level: "category"},
		{ID: pick.ID.String(), Name: "Colour", FieldType: "select", Options: []string{"red"}, Level: "category"},
	}, got, "the fields the new-ticket form shows: shown and active, in the form's order")
	for _, s := range []string{"Staff note", "Gone", "Asset tag", "blue", "retired"} {
		require.NotContains(t, text, s)
	}

	withType, _ := list(map[string]any{"category_id": h.catID.String(), "type_id": typ.ID.String()})
	require.Len(t, withType, 3)
	require.Equal(t, "Asset tag", withType[2].Name)
	require.Equal(t, "type", withType[2].Level)

	for name, args := range map[string]map[string]any{
		"no category": {}, "a bad category": {"category_id": "x"}, "a bad type": {"category_id": h.catID.String(), "type_id": "x"},
	} {
		text, isErr := s.tool("list_ticket_fields", args)
		require.True(t, isErr, "%s: %s", name, text)
	}
	empty, _ := list(map[string]any{"category_id": uuid.NewString()})
	require.Empty(t, empty, "a category with no fields is an empty list, not an error")
}

func TestMCPGetTicket_ReadsCustomFieldsBack(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := mcpFieldSession(t, h)
	serial := h.mkField(t, "Serial", customfield.FieldTypeText, nil, 1)
	h.attach(t, serial, customfield.CategoryScope, h.catID, 1)

	text, isErr := h.mcpCreate(t, s, map[string]any{serial.ID.String(): "SN-1"})
	require.False(t, isErr, text)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &created))

	got, isErr := s.tool("get_ticket", map[string]any{"id": created.ID})
	require.False(t, isErr, got)
	var out struct {
		CustomFields []struct {
			FieldDefID string `json:"field_def_id"`
			FieldName  string `json:"field_name"`
			Value      string `json:"value"`
		} `json:"custom_fields"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &out))
	require.Len(t, out.CustomFields, 1)
	require.Equal(t, "Serial", out.CustomFields[0].FieldName)
	require.Equal(t, "SN-1", out.CustomFields[0].Value)

	t.Run("a ticket with none keeps the response as it was", func(t *testing.T) {
		bare := h.newTicket(t, nil, nil)
		got, isErr := s.tool("get_ticket", map[string]any{"id": bare.ID.String()})
		require.False(t, isErr, got)
		require.NotContains(t, got, "custom_fields")
	})
}
