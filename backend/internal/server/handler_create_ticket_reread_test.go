package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// POST /tickets routes the new ticket after creating it. The 201 used to be
// the ticket as it was before routing, so a caller saw no assignee until it
// asked again; handleCreateFollowUp already reads back, and this must match.
func TestCreateTicket_ResponseCarriesTheRoutedAssignee(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	g, err := h.groupSvc.Create(ctx, "Routing target", "")
	require.NoError(t, err)
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAutoAssignGroupID, g.ID.String()))

	res := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "routed", "description": "x", "category_id": h.catID.String(),
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var got ticket.Ticket
	require.NoError(t, json.NewDecoder(res.Body).Decode(&got))
	require.NotNil(t, got.AssigneeGroupID, "the 201 must show the group routing assigned")
	require.Equal(t, g.ID, *got.AssigneeGroupID)
}
