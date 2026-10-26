package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

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
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
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

// When a ticket went Pending and how long it has been paused are audit
// information, not state (Erik, 2026-10-15): staff and administrators only, in
// every response, MCP included.
func TestRequester_GetsNoPauseFields(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := reopenedTicketOf(t, h)
	cur, err := h.ticketStore.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	now := time.Now()
	cur.PendingSince, cur.SLAPausedSeconds = &now, 120
	require.NoError(t, h.ticketStore.Update(ctx, cur))
	path := "/api/v1/tickets/" + tk.ID.String()

	decode := func(res *http.Response) map[string]any {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		return m
	}
	decodeList := func(res *http.Response) []map[string]any {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		var m []map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		return m
	}

	t.Run("HTTP: a requester gets neither key", func(t *testing.T) {
		m := decode(h.doAsUser(t, http.MethodGet, path, nil))
		require.NotContains(t, m, "sla_paused_seconds", "GET")
		require.NotContains(t, m, "pending_since", "GET")
		for _, row := range decodeList(h.doAsUser(t, http.MethodGet, "/api/v1/tickets", nil)) {
			require.NotContains(t, row, "sla_paused_seconds", "list")
			require.NotContains(t, row, "pending_since", "list")
		}
		m = decode(h.doAsUser(t, http.MethodPatch, path, map[string]any{}))
		require.NotContains(t, m, "sla_paused_seconds", "PATCH")
		require.NotContains(t, m, "pending_since", "PATCH")
	})

	t.Run("HTTP: staff and administrators still do", func(t *testing.T) {
		for name, do := range map[string]func(*testing.T, string, string, any) *http.Response{"staff": h.do, "admin": h.doAsAdmin} {
			m := decode(do(t, http.MethodGet, path, nil))
			require.EqualValues(t, 120, m["sla_paused_seconds"], name)
			require.Contains(t, m, "pending_since", name)
		}
		var found bool
		for _, row := range decodeList(h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?reporter_id="+h.userID.String(), nil)) {
			if row["id"] == tk.ID.String() {
				found = true
				require.EqualValues(t, 120, row["sla_paused_seconds"])
				require.Contains(t, row, "pending_since")
			}
		}
		require.True(t, found)
	})

	for _, tr := range mcpTransports {
		t.Run("MCP "+tr.name, func(t *testing.T) {
			req := tr.open(t, h, h.userKey)
			got := req.call(t, "get_ticket", map[string]any{"id": tk.ID.String()})
			require.Contains(t, got, "Printer", got)
			require.NotContains(t, got, "sla_paused_seconds", "get_ticket")
			require.NotContains(t, got, "pending_since", "get_ticket")
			got = req.call(t, "list_tickets", map[string]any{})
			require.Contains(t, got, "Printer", got)
			require.NotContains(t, got, "sla_paused_seconds", "list_tickets")
			require.NotContains(t, got, "pending_since", "list_tickets")
		})
	}
}
