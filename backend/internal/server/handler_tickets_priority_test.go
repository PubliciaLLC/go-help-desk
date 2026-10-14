package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// PATCH /tickets/{id} {priority} (#443b): staff and admin change a ticket's
// priority, the SLA re-matches, and the audit feed says so.
func TestUpdateTicket_Priority_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	mk := func(reporter uuid.UUID) ticket.Ticket {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
			Subject: "Printer", CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &reporter,
		})
		require.NoError(t, err)
		return tk
	}
	tk := mk(h.userID)
	path := "/api/v1/tickets/" + tk.ID.String()

	priority := func(t *testing.T) ticket.Priority {
		t.Helper()
		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		return got.Priority
	}

	t.Run("403: a reporting user may not change priority, and nothing is written", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodPatch, path, map[string]any{"priority": "critical"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)
		require.Equal(t, ticket.PriorityMedium, priority(t))
	})

	t.Run("400: a value that is not a priority", func(t *testing.T) {
		res := h.do(t, http.MethodPatch, path, map[string]any{"priority": "urgent"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", b)
		require.Equal(t, ticket.PriorityMedium, priority(t))
	})

	t.Run("404: a ticket the caller cannot see answers before the role does", func(t *testing.T) {
		other := mk(h.staffID) // reported by someone else
		res := h.doAsUser(t, http.MethodPatch, "/api/v1/tickets/"+other.ID.String(), map[string]any{"priority": "high"})
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	})

	t.Run("200: staff change it and the body carries the new priority", func(t *testing.T) {
		res := h.do(t, http.MethodPatch, path, map[string]any{"priority": "high"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)

		var got ticket.Ticket
		require.NoError(t, json.Unmarshal(b, &got))
		require.Equal(t, ticket.PriorityHigh, got.Priority)
		require.Equal(t, ticket.PriorityHigh, priority(t))
	})

	t.Run("the audit feed shows priority_changed with before and after", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodGet, path+"/audit", nil)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)

		var entries []struct {
			Action string         `json:"action"`
			Before map[string]any `json:"before"`
			After  map[string]any `json:"after"`
		}
		require.NoError(t, json.Unmarshal(b, &entries))
		var found bool
		for _, e := range entries {
			if e.Action != "priority_changed" {
				continue
			}
			found = true
			require.Equal(t, "medium", e.Before["priority"])
			require.Equal(t, "high", e.After["priority"])
			require.Equal(t, "manual", e.After["priority_change_reason"])
			require.Equal(t, "sla_off", e.After["sla_outcome"], "the harness never switches the SLA on")
			require.NotContains(t, e.After, "subject", "free text stays out of the feed")
		}
		require.True(t, found, "entries: %s", b)
	})

	t.Run("200: the same priority again is accepted and writes nothing more", func(t *testing.T) {
		before, err := h.ticketSvc.ListAuditEntries(ctx, tk.ID)
		require.NoError(t, err)

		res := h.do(t, http.MethodPatch, path, map[string]any{"priority": "high"})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		after, err := h.ticketSvc.ListAuditEntries(ctx, tk.ID)
		require.NoError(t, err)
		require.Len(t, after, len(before))
	})

	t.Run("200: status and priority in one request apply both", func(t *testing.T) {
		inProgress, err := h.ticketStore.GetStatusByName(ctx, "In Progress")
		require.NoError(t, err)

		res := h.do(t, http.MethodPatch, path, map[string]any{"status_id": inProgress.ID, "priority": "low"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)

		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		require.Equal(t, inProgress.ID, got.StatusID)
		require.Equal(t, ticket.PriorityLow, got.Priority)
	})

	t.Run("a PATCH without priority leaves it alone", func(t *testing.T) {
		res := h.do(t, http.MethodPatch, path, map[string]any{"clear_assignee": true})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, ticket.PriorityLow, priority(t))
	})
}
