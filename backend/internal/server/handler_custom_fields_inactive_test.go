package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// An inactive field is out of circulation: nobody may write to it, and that
// includes writing nothing. A clear used to slip through because the empty-value
// branch ran before the active check, so a deactivated field could still have
// its stored value wiped by a caller who sent "".

// inactiveFieldOnTicket builds a ticket whose category carries a text field,
// stores value on it (when non-empty), then deactivates the field. It returns
// the ticket id and the field id.
func inactiveFieldOnTicket(t *testing.T, h *harness, value string) (ticketID, fieldID string) {
	t.Helper()
	ctx := context.Background()

	def, err := h.customFieldSvc.CreateFieldDef(ctx, "Cost centre", customfield.FieldTypeText, nil, 1, nil)
	require.NoError(t, err)
	_, err = h.customFieldSvc.CreateAssignment(ctx, def.ID, customfield.CategoryScope, h.catID, 1, true, false, nil)
	require.NoError(t, err)

	res := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "Has a field", "description": "x", "category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, res, &created)
	require.NotEmpty(t, created.ID)

	if value != "" {
		put := h.do(t, http.MethodPut, "/api/v1/tickets/"+created.ID+"/custom-fields",
			map[string]string{def.ID.String(): value})
		put.Body.Close()
		require.Equal(t, http.StatusNoContent, put.StatusCode, "the value has to be stored while the field is still active")
	}

	def.Active = false
	require.NoError(t, h.customFieldSvc.UpdateFieldDef(ctx, def, nil))
	return created.ID, def.ID.String()
}

func TestPutCustomFields_InactiveFieldRefusesAValue(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ticketID, fieldID := inactiveFieldOnTicket(t, h, "")

	res := h.do(t, http.MethodPut, "/api/v1/tickets/"+ticketID+"/custom-fields",
		map[string]string{fieldID: "CC-100"})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestPutCustomFields_InactiveFieldRefusesAClear(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ticketID, fieldID := inactiveFieldOnTicket(t, h, "CC-100")

	res := h.do(t, http.MethodPut, "/api/v1/tickets/"+ticketID+"/custom-fields",
		map[string]string{fieldID: ""})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode,
		"a clear is a write; an inactive field takes none")
}

func TestPutCustomFields_InactiveFieldKeepsItsStoredValue(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ticketID, fieldID := inactiveFieldOnTicket(t, h, "CC-100")

	for _, v := range []string{"", "CC-200"} {
		res := h.do(t, http.MethodPut, "/api/v1/tickets/"+ticketID+"/custom-fields",
			map[string]string{fieldID: v})
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
	}

	res := h.do(t, http.MethodGet, "/api/v1/tickets/"+ticketID+"/custom-fields", nil)
	defer res.Body.Close()
	var got []customfield.TicketFieldValue
	decodeJSON(t, res, &got)
	require.Len(t, got, 1)
	require.Equal(t, "CC-100", got[0].Value, "a refused write left the stored value alone")
}
