package server_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// "A refused request changes nothing" (#443, confirmed on #290) for a reporting
// user who sends a priority together with another change. Each case names a
// second change the reporter may not make either; whichever refusal answers,
// the ticket's status, assignee, classification and priority are as they were
// and no audit row was written. Staff sending the same body still succeed.
func TestUpdateTicket_ReporterPriorityRequestChangesNothing_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	inProgress, err := h.ticketStore.GetStatusByName(ctx, "In Progress")
	require.NoError(t, err)
	otherCat, err := h.categorySvc.CreateCategory(ctx, "Other", 2)
	require.NoError(t, err)

	mk := func() ticket.Ticket {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
			Subject: "Printer", CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
		})
		require.NoError(t, err)
		return tk
	}

	cases := []struct {
		name       string
		body       map[string]any
		wantStatus int
	}{
		{"status and priority", map[string]any{"status_id": inProgress.ID, "priority": "critical"}, http.StatusForbidden},
		{"assignee and priority", map[string]any{"assignee_user_id": h.staffID, "priority": "critical"}, http.StatusForbidden},
		{"clear_assignee and priority", map[string]any{"clear_assignee": true, "priority": "critical"}, http.StatusForbidden},
		{"category and priority", map[string]any{"category_id": otherCat.ID, "priority": "critical"}, http.StatusForbidden},
		{"priority alone", map[string]any{"priority": "critical"}, http.StatusForbidden},
		// A value that is not a priority is a malformed request: 400 is
		// decided before the role check, as the invalid-priority check
		// sits ahead of every role check in handleUpdateTicket.
		{"invalid priority alone", map[string]any{"priority": "urgent"}, http.StatusBadRequest},
		{"status and invalid priority", map[string]any{"status_id": inProgress.ID, "priority": "urgent"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run("reporter: "+tc.name, func(t *testing.T) {
			tk := mk()
			before, err := h.ticketSvc.ListAuditEntries(ctx, tk.ID)
			require.NoError(t, err)

			res := h.doAsUser(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), tc.body)
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			require.Equal(t, tc.wantStatus, res.StatusCode, "body: %s", b)

			got, err := h.ticketSvc.GetByID(ctx, tk.ID)
			require.NoError(t, err)
			require.Equal(t, tk.StatusID, got.StatusID)
			require.Equal(t, tk.AssigneeUserID, got.AssigneeUserID)
			require.Equal(t, tk.AssigneeGroupID, got.AssigneeGroupID)
			require.Equal(t, tk.CategoryID, got.CategoryID)
			require.Equal(t, ticket.PriorityMedium, got.Priority)

			after, err := h.ticketSvc.ListAuditEntries(ctx, tk.ID)
			require.NoError(t, err)
			require.Len(t, after, len(before), "a refused request writes no audit row")
		})
	}

	t.Run("staff: the same bodies still succeed", func(t *testing.T) {
		for _, body := range []map[string]any{
			{"status_id": inProgress.ID, "priority": "critical"},
			{"assignee_user_id": h.staffID, "priority": "high"},
			{"category_id": otherCat.ID, "priority": "low"},
		} {
			tk := mk()
			res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), body)
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)

			got, err := h.ticketSvc.GetByID(ctx, tk.ID)
			require.NoError(t, err)
			require.Equal(t, ticket.Priority(body["priority"].(string)), got.Priority)
		}
	})
}

// A priority change on a Resolved or Closed ticket is refused with 409
// ticket_closed. When it rides along with another change, that refusal must
// come before the other change is saved, not after: a refused request changes
// nothing. Two ways to get there: the request itself moves the ticket into
// Resolved or Closed, or the ticket is already there and stays there.
func TestUpdateTicket_PriorityOnFinishedTicketChangesNothing_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	admin := ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}
	resolvedSt, err := h.ticketStore.GetStatusByName(ctx, "Resolved")
	require.NoError(t, err)
	closedSt, err := h.ticketStore.GetStatusByName(ctx, "Closed")
	require.NoError(t, err)
	inProgress, err := h.ticketStore.GetStatusByName(ctx, "In Progress")
	require.NoError(t, err)

	mk := func() ticket.Ticket {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
			Subject: "Printer", CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
		})
		require.NoError(t, err)
		return tk
	}
	mkResolved := func() ticket.Ticket {
		tk := mk()
		_, err := h.ticketSvc.Resolve(ctx, tk.ID, "done", staff)
		require.NoError(t, err)
		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		return got
	}
	mkClosed := func() ticket.Ticket {
		tk := mkResolved()
		require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, admin))
		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		return got
	}

	refused := func(t *testing.T, do func(*testing.T, string, string, any) *http.Response, tk ticket.Ticket, body map[string]any) {
		t.Helper()
		before, err := h.ticketSvc.ListAuditEntries(ctx, tk.ID)
		require.NoError(t, err)

		res := do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), body)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusConflict, res.StatusCode, "body: %s", b)
		require.Contains(t, string(b), "ticket_closed")

		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		require.Equal(t, tk.StatusID, got.StatusID, "status")
		require.Equal(t, tk.AssigneeUserID, got.AssigneeUserID, "assignee")
		require.Equal(t, tk.Priority, got.Priority, "priority")
		after, err := h.ticketSvc.ListAuditEntries(ctx, tk.ID)
		require.NoError(t, err)
		require.Len(t, after, len(before), "a refused request writes no audit row")
	}

	t.Run("staff: Resolved and priority", func(t *testing.T) {
		refused(t, h.do, mk(), map[string]any{"status_id": resolvedSt.ID, "priority": "high"})
	})
	t.Run("admin: Resolved and priority", func(t *testing.T) {
		refused(t, h.doAsAdmin, mk(), map[string]any{"status_id": resolvedSt.ID, "priority": "high"})
	})
	t.Run("admin: Closed and priority", func(t *testing.T) {
		refused(t, h.doAsAdmin, mk(), map[string]any{"status_id": closedSt.ID, "priority": "high"})
	})
	t.Run("staff: assignment and priority on a Resolved ticket", func(t *testing.T) {
		refused(t, h.do, mkResolved(), map[string]any{"assignee_user_id": h.staffID, "priority": "high"})
	})
	t.Run("admin: assignment and priority on a Closed ticket", func(t *testing.T) {
		refused(t, h.doAsAdmin, mkClosed(), map[string]any{"assignee_user_id": h.staffID, "priority": "high"})
	})
	t.Run("admin: clear_assignee and priority on a Resolved ticket", func(t *testing.T) {
		refused(t, h.doAsAdmin, mkResolved(), map[string]any{"clear_assignee": true, "priority": "high"})
	})
	t.Run("staff: staying in Resolved and priority", func(t *testing.T) {
		refused(t, h.do, mkResolved(), map[string]any{"status_id": resolvedSt.ID, "priority": "high"})
	})

	t.Run("200: In Progress and priority apply both", func(t *testing.T) {
		tk := mk()
		res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(),
			map[string]any{"status_id": inProgress.ID, "priority": "high"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		require.Equal(t, inProgress.ID, got.StatusID)
		require.Equal(t, ticket.PriorityHigh, got.Priority)
	})

	t.Run("200: moving a Resolved ticket out of Resolved with a priority is not refused", func(t *testing.T) {
		tk := mkResolved()
		res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(),
			map[string]any{"status_id": inProgress.ID, "priority": "high"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		require.Equal(t, inProgress.ID, got.StatusID, "the request leaves the ticket open, so the priority applies too")
		require.Equal(t, ticket.PriorityHigh, got.Priority)
	})

	// The rule (b) lets a request through when it moves the ticket out of
	// Closed; whether that move is allowed is closed_reopen_policy's call
	// (UpdateStatus), and the harness runs the default, "off". So this is the
	// policy's 409, with nothing saved, not the priority rule's.
	t.Run("a Closed ticket moved to In Progress with a priority: the reopen policy answers, nothing is saved", func(t *testing.T) {
		tk := mkClosed()
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(),
			map[string]any{"status_id": inProgress.ID, "priority": "high"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusConflict, res.StatusCode, "body: %s", b)
		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		require.Equal(t, closedSt.ID, got.StatusID)
		require.Equal(t, ticket.PriorityMedium, got.Priority)
	})
}
