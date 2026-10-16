package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Round-robin advances only when a ticket was actually routed (F7). Routing is
// worked out before the create now, so a refused create must not use up a turn:
// the next ticket gets the person the refused one would have had.
func TestRoundRobin_ARefusedCreateDoesNotAdvanceIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	status, body := patchSettings(t, h, map[string]any{
		"auto_assign_user_ids": []string{h.staffID.String(), h.adminID.String()},
	})
	require.Equal(t, http.StatusNoContent, status, body)

	first := createTicketAsUser(t, h, "First")
	firstHeld, err := h.ticketSvc.GetByID(ctx, first.ID)
	require.NoError(t, err)
	require.NotNil(t, firstHeld.AssigneeUserID)

	// Refused by the service after routing was worked out: an unknown category.
	res := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "Nowhere", "description": "x", "category_id": "11111111-1111-1111-1111-111111111111",
	})
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	second := createTicketAsUser(t, h, "Second")
	secondHeld, err := h.ticketSvc.GetByID(ctx, second.ID)
	require.NoError(t, err)
	require.NotNil(t, secondHeld.AssigneeUserID)
	require.NotEqual(t, *firstHeld.AssigneeUserID, *secondHeld.AssigneeUserID,
		"the refused create used up a turn, so the same person got two in a row")
}
