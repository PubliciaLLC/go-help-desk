package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// What a requester sees of their own ticket (Erik, 2026-10-15): the current
// state and the conversation, not history, not the audit trail, and not the
// staff-only reopen count.

// reopenedTicketOf files a ticket for the seeded requester, resolves it and has
// staff reopen it, so reopen_count is 1.
func reopenedTicketOf(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	ctx := context.Background()
	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Printer", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	_, err = h.ticketSvc.Resolve(ctx, tk.ID, "done", staff)
	require.NoError(t, err)
	inProgress, err := h.ticketStore.GetStatusByName(ctx, "In Progress")
	require.NoError(t, err)
	_, err = h.ticketSvc.UpdateStatus(ctx, tk.ID, inProgress.ID, staff)
	require.NoError(t, err)
	return tk
}

// The status timeline and the audit feed are history. A requester is refused
// both on their own ticket (403, as for the other staff-only ticket routes: tags,
// followers), and staff and administrators still get them. A ticket the caller
// cannot see still answers 404 before the role is asked.
func TestRequester_GetsNoHistoryOrAuditFeed_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := reopenedTicketOf(t, h)
	base := "/api/v1/tickets/" + tk.ID.String()

	for _, path := range []string{"/audit", "/history"} {
		t.Run(path, func(t *testing.T) {
			res := h.doAsUser(t, http.MethodGet, base+path, nil)
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode, "requester: body %s", b)

			res = h.do(t, http.MethodGet, base+path, nil)
			res.Body.Close()
			require.Equal(t, http.StatusOK, res.StatusCode, "staff")
			res = h.doAsAdmin(t, http.MethodGet, base+path, nil)
			res.Body.Close()
			require.Equal(t, http.StatusOK, res.StatusCode, "admin")
		})
	}

	// The conversation stays: it is the ticket's content, not its history.
	res := h.doAsUser(t, http.MethodGet, base+"/replies", nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

// A requester's PATCH answers with the ticket, and the ticket they get back
// carries no reopen_count.
func TestRequester_PatchResponseHasNoReopenCount_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := reopenedTicketOf(t, h)

	res := h.doAsUser(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{})
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	require.NotContains(t, m, "reopen_count")

	res = h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{})
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	require.NoError(t, json.Unmarshal(b, &m))
	require.EqualValues(t, 1, m["reopen_count"], "staff")
}

// MCP hands a requester ticket JSON too: get_ticket, list_tickets and
// create_ticket marshal the ticket themselves, so each is covered.
func TestRequester_MCPHasNoReopenCount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := reopenedTicketOf(t, h)

	for _, tr := range mcpTransports {
		t.Run(tr.name, func(t *testing.T) {
			req := tr.open(t, h, h.userKey)

			got := req.call(t, "get_ticket", map[string]any{"id": tk.ID.String()})
			require.Contains(t, got, "Printer", "the requester reads their own ticket: %s", got)
			require.NotContains(t, got, "reopen_count", "get_ticket")

			got = req.call(t, "list_tickets", map[string]any{})
			require.Contains(t, got, "Printer", "list_tickets: %s", got)
			require.NotContains(t, got, "reopen_count", "list_tickets")
		})
	}
}
