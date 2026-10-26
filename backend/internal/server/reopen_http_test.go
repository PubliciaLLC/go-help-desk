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

// Reopen over HTTP (#464): both ways a person reopens a ticket through the API
// flag it (reopen_count in the JSON), raise the priority once, and leave the
// audit entries the activity feed shows.
func TestReopen_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}

	mkResolved := func() ticket.Ticket {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
			Subject: "Printer", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
		})
		require.NoError(t, err)
		_, err = h.ticketSvc.Resolve(ctx, tk.ID, "done", staff)
		require.NoError(t, err)
		return tk
	}
	get := func(t *testing.T, id string) ticket.Ticket {
		t.Helper()
		res := h.do(t, http.MethodGet, "/api/v1/tickets/"+id, nil)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		var got ticket.Ticket
		require.NoError(t, json.Unmarshal(b, &got))
		return got
	}
	actions := func(t *testing.T, id string) map[string]int {
		t.Helper()
		res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id+"/audit", nil)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		var entries []struct {
			Action string `json:"action"`
		}
		require.NoError(t, json.Unmarshal(b, &entries))
		out := map[string]int{}
		for _, e := range entries {
			out[e.Action]++
		}
		return out
	}

	t.Run("a requester's reply on a Resolved ticket", func(t *testing.T) {
		tk := mkResolved()
		require.Zero(t, get(t, tk.ID.String()).ReopenCount)

		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies", map[string]any{"body": "still broken"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)

		got := get(t, tk.ID.String())
		require.Equal(t, 1, got.ReopenCount)
		require.Equal(t, ticket.PriorityMedium, got.Priority, "low goes up one level")
		a := actions(t, tk.ID.String())
		require.Equal(t, 1, a["reopened"])
		require.Equal(t, 1, a["priority_changed"])
	})

	t.Run("staff changing a Resolved ticket's status, twice", func(t *testing.T) {
		tk := mkResolved()
		inProgress, err := h.ticketStore.GetStatusByName(ctx, "In Progress")
		require.NoError(t, err)
		path := "/api/v1/tickets/" + tk.ID.String()

		res := h.do(t, http.MethodPatch, path, map[string]any{"status_id": inProgress.ID})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		var patched ticket.Ticket
		require.NoError(t, json.Unmarshal(b, &patched))
		require.Equal(t, 1, patched.ReopenCount, "the PATCH response carries the flag")
		require.Equal(t, ticket.PriorityMedium, patched.Priority)

		_, err = h.ticketSvc.Resolve(ctx, tk.ID, "done again", staff)
		require.NoError(t, err)
		res = h.do(t, http.MethodPatch, path, map[string]any{"status_id": inProgress.ID})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		got := get(t, tk.ID.String())
		require.Equal(t, 2, got.ReopenCount)
		require.Equal(t, ticket.PriorityMedium, got.Priority, "only the first reopen bumps")
		a := actions(t, tk.ID.String())
		require.Equal(t, 2, a["reopened"])
		require.Equal(t, 1, a["priority_changed"])
	})

	t.Run("a refused reopen of a Closed ticket changes nothing", func(t *testing.T) {
		tk := mkResolved()
		require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
		inProgress, err := h.ticketStore.GetStatusByName(ctx, "In Progress")
		require.NoError(t, err)

		res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"status_id": inProgress.ID})
		res.Body.Close()
		require.Equal(t, http.StatusConflict, res.StatusCode, "closed_reopen_policy defaults to off")

		got := get(t, tk.ID.String())
		require.Zero(t, got.ReopenCount)
		require.Equal(t, ticket.PriorityLow, got.Priority)
		require.Zero(t, actions(t, tk.ID.String())["priority_changed"])
	})
}

// reopen_count is staff information (Erik, 2026-10-14): a requester's view of
// their own ticket, one or many, has no such key; staff and administrators have
// it.
func TestReopen_CountIsForStaffOnly_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
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

	one := func(res *http.Response) map[string]any {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		return m
	}
	list := func(res *http.Response) []map[string]any {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		var m []map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		return m
	}
	path := "/api/v1/tickets/" + tk.ID.String()

	require.NotContains(t, one(h.doAsUser(t, http.MethodGet, path, nil)), "reopen_count", "requester GET")
	for _, row := range list(h.doAsUser(t, http.MethodGet, "/api/v1/tickets", nil)) {
		require.NotContains(t, row, "reopen_count", "requester list")
	}
	require.EqualValues(t, 1, one(h.do(t, http.MethodGet, path, nil))["reopen_count"], "staff GET")
	found := false
	for _, row := range list(h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?reporter_id="+h.userID.String(), nil)) {
		if row["id"] == tk.ID.String() {
			found = true
			require.EqualValues(t, 1, row["reopen_count"], "admin list")
		}
	}
	require.True(t, found)
}
