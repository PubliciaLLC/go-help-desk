package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// PATCH /tickets/{id} {priority} (#443b): staff and admin change a ticket's
// priority, the SLA re-matches, and the audit feed says so.
func TestUpdateTicket_Priority_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	mk := func(reporter uuid.UUID) ticket.Ticket {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain,
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
		before, err := h.ticketSvc.ListAuditFeed(ctx, tk.ID, true)
		require.NoError(t, err)

		res := h.do(t, http.MethodPatch, path, map[string]any{"priority": "high"})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		after, err := h.ticketSvc.ListAuditFeed(ctx, tk.ID, true)
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

	t.Run("409: a Resolved or Closed ticket is refused, priority included", func(t *testing.T) {
		staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
		admin := ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}

		resolved := mk(h.userID)
		_, err := h.ticketSvc.Resolve(ctx, resolved.ID, "done", staff)
		require.NoError(t, err)
		closed := mk(h.userID)
		_, err = h.ticketSvc.Resolve(ctx, closed.ID, "done", staff)
		require.NoError(t, err)
		require.NoError(t, h.ticketSvc.Close(ctx, closed.ID, admin))

		for name, tk := range map[string]ticket.Ticket{"resolved": resolved, "closed": closed} {
			res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"priority": "critical"})
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			require.Equal(t, http.StatusConflict, res.StatusCode, "%s: body: %s", name, b)
			require.Contains(t, string(b), "ticket_closed")

			got, err := h.ticketSvc.GetByID(ctx, tk.ID)
			require.NoError(t, err)
			require.Equal(t, ticket.PriorityMedium, got.Priority, "%s: nothing is written", name)
		}
	})
}

// A priority that is not a priority is a malformed request: it answers 400
// before anything else in the same PATCH is written, rather than after the
// status change ahead of it has already been saved.
func TestUpdateTicket_InvalidPriorityWritesNothing_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain,
		Subject: "Printer", CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	inProgress, err := h.ticketStore.GetStatusByName(ctx, "In Progress")
	require.NoError(t, err)

	res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(),
		map[string]any{"status_id": inProgress.ID, "priority": "urgent"})
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", b)

	got, err := h.ticketSvc.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	require.Equal(t, tk.StatusID, got.StatusID, "the status ahead of the bad priority is not saved")
	require.Equal(t, ticket.PriorityMedium, got.Priority)
}

// A machine credential needs the tickets write scope to change a priority, like
// any other PATCH; with it, a staff member's key acts as that staff member.
func TestUpdateTicket_Priority_APIKeyScopes_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain,
		Subject: "Printer", CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	key := func(scopes ...auth.Scope) string {
		raw, hashed, err := auth.GenerateToken()
		require.NoError(t, err)
		names := make([]string, len(scopes))
		for i, s := range scopes {
			names[i] = s.String()
		}
		require.NoError(t, h.authStore.CreateAPIKey(ctx, auth.APIKey{
			ID: uuid.New(), Name: "priority", HashedToken: hashed, UserID: h.staffID,
			Scopes: names, CreatedAt: time.Now(),
		}))
		return raw
	}
	patch := func(k string) *http.Response {
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), strings.NewReader(`{"priority":"high"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "ApiKey "+k)
		rr := httptest.NewRecorder()
		h.srv.ServeHTTP(rr, req)
		return rr.Result()
	}
	priority := func() ticket.Priority {
		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		return got.Priority
	}

	res := patch(key(auth.Scope{Resource: auth.ResourceTickets, Action: auth.ActionRead}))
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode, "a read-only key cannot change the priority")
	require.Equal(t, ticket.PriorityMedium, priority())

	res = patch(key(auth.Scope{Resource: auth.ResourceTickets, Action: auth.ActionWrite}))
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	require.Equal(t, ticket.PriorityHigh, priority())
}
