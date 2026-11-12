package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Every door that creates a ticket routes it the same way (1.3.0 audit D2).
// POST /tickets and the two REST follow-ups always did; the guest form and the
// two MCP tools created tickets nobody was ever handed, so the queue a routing
// rule was meant to fill stayed empty for a ticket filed through them. The rule
// is the server's one `routing`, so these only check that each door asks it.

func routeEverythingTo(t *testing.T, h *harness) *ticket.Assignment {
	t.Helper()
	ctx := context.Background()
	g, err := h.groupSvc.Create(ctx, "Routing target", "")
	require.NoError(t, err)
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAutoAssignGroupID, g.ID.String()))
	return &ticket.Assignment{GroupID: &g.ID}
}

func ticketNamed(t *testing.T, h *harness, subject string) ticket.Ticket {
	t.Helper()
	all, err := h.ticketSvc.ListFiltered(context.Background(), ticket.Filter{Visibility: ticket.VisibilityAll, Limit: 200})
	require.NoError(t, err)
	for _, tk := range all {
		if tk.Subject == subject {
			return tk
		}
	}
	require.FailNow(t, "no ticket with that subject", subject)
	return ticket.Ticket{}
}

func TestAutoAssign_GuestSubmissionIsRouted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	want := routeEverythingTo(t, h)

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", map[string]any{
		"subject": "Guest door", "description": "x",
		"category_id": h.catID.String(), "guest_email": "walkin@example.test", "guest_name": "Ada",
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	got := ticketNamed(t, h, "Guest door")
	require.NotNil(t, got.AssigneeGroupID, "a guest's ticket is routed like any other")
	require.Equal(t, *want.GroupID, *got.AssigneeGroupID)
}

func TestAutoAssign_MCPCreateTicketIsRouted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	want := routeEverythingTo(t, h)

	c := openMCP(t, h, h.apiKey)
	defer c.closeBody()
	out := c.call(t, "create_ticket", map[string]any{
		"subject": "MCP door", "description": "x", "category_id": h.catID.String(),
	})
	require.NotContains(t, out, `"isError":true`, out)

	got := ticketNamed(t, h, "MCP door")
	require.NotNil(t, got.AssigneeGroupID, "a ticket filed over MCP is routed like any other")
	require.Equal(t, *want.GroupID, *got.AssigneeGroupID)
}

func TestAutoAssign_MCPCreateFollowUpIsRouted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	closed := reporterClosedTicket(t, h)
	want := routeEverythingTo(t, h)

	c := openMCP(t, h, h.apiKey)
	defer c.closeBody()
	out := c.call(t, "create_follow_up", map[string]any{"ticket_id": closed.ID.String()})
	require.NotContains(t, out, `"isError":true`, out)

	links, err := h.ticketSvc.ListLinks(ctx, closed.ID)
	require.NoError(t, err)
	require.NotEmpty(t, links)
	got, err := h.ticketSvc.GetByID(ctx, links[len(links)-1].TargetTicketID)
	require.NoError(t, err)
	require.NotNil(t, got.AssigneeGroupID, "a follow-up is a new ticket and is routed like one")
	require.Equal(t, *want.GroupID, *got.AssigneeGroupID)
}
