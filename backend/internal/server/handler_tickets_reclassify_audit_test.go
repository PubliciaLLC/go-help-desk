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
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// PATCH /tickets/{id} {category_id} is audited (1.3.0 audit D3): the feed says
// who moved the ticket and between which categories, by id, and the audit view
// does not show the ticket's free text beside it.
func TestUpdateTicket_Reclassify_IsAuditedHTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	other, err := h.categorySvc.CreateCategory(ctx, "Reclassify target", 91)
	require.NoError(t, err)
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Printer", CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	path := "/api/v1/tickets/" + tk.ID.String()

	res := h.do(t, http.MethodPatch, path, map[string]any{"category_id": other.ID.String()})
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)

	res = h.doAsAdmin(t, http.MethodGet, path+"/audit", nil)
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	var entries []struct {
		Action  string         `json:"action"`
		ActorID *string        `json:"actor_id"`
		Before  map[string]any `json:"before"`
		After   map[string]any `json:"after"`
	}
	require.NoError(t, json.Unmarshal(b, &entries))
	var found bool
	for _, e := range entries {
		if e.Action != "classified" {
			continue
		}
		found = true
		require.NotNil(t, e.ActorID)
		require.Equal(t, h.staffID.String(), *e.ActorID)
		require.Equal(t, h.catID.String(), e.Before["category_id"])
		require.Equal(t, other.ID.String(), e.After["category_id"])
		require.Equal(t, "sla_off", e.After["sla_outcome"], "the harness never switches the SLA on")
		require.NotContains(t, e.After, "subject", "free text stays out of the feed")
	}
	require.True(t, found, "entries: %s", b)

	// A request that changes nothing adds nothing.
	res = h.do(t, http.MethodPatch, path, map[string]any{"category_id": other.ID.String()})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	after, err := h.ticketSvc.ListAuditFeed(ctx, tk.ID, true)
	require.NoError(t, err)
	var n int
	for _, e := range after {
		if e.Action == "classified" {
			n++
		}
	}
	require.Equal(t, 1, n)
}

// Classification only happens to open tickets: a Resolved or Closed ticket
// refuses a change of category, type or item with 409 ticket_closed, for staff
// and for an administrator, and nothing is written. A PATCH that touches no
// classification field behaves as it always did.
func TestUpdateTicket_Reclassify_RefusedOnResolvedAndClosedHTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	other, err := h.categorySvc.CreateCategory(ctx, "Reclassify refused", 92)
	require.NoError(t, err)
	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	admin := ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}

	for name, finish := range map[string]func(id uuid.UUID) error{
		"resolved": func(id uuid.UUID) error { _, err := h.ticketSvc.Resolve(ctx, id, "done", staff); return err },
		"closed":   func(id uuid.UUID) error { return h.ticketSvc.Close(ctx, id, admin) },
	} {
		t.Run(name, func(t *testing.T) {
			tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
				Subject: "Done", CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
			})
			require.NoError(t, err)
			require.NoError(t, finish(tk.ID))
			path := "/api/v1/tickets/" + tk.ID.String()
			feedBefore, err := h.ticketSvc.ListAuditFeed(ctx, tk.ID, true)
			require.NoError(t, err)

			for who, do := range map[string]func(*testing.T, string, string, any) *http.Response{
				"staff": h.do, "admin": h.doAsAdmin,
			} {
				for what, body := range map[string]map[string]any{
					"a new category":                    {"category_id": other.ID.String()},
					"the same category, a type cleared": {"category_id": h.catID.String(), "type_id": nil},
				} {
					res := do(t, http.MethodPatch, path, body)
					b, _ := io.ReadAll(res.Body)
					res.Body.Close()
					require.Equal(t, http.StatusConflict, res.StatusCode, "%s, %s: %s", who, what, b)
					require.Contains(t, string(b), "ticket_closed")
				}
			}

			got, err := h.ticketSvc.GetByID(ctx, tk.ID)
			require.NoError(t, err)
			require.Equal(t, h.catID, got.CategoryID, "nothing was written")
			feedAfter, err := h.ticketSvc.ListAuditFeed(ctx, tk.ID, true)
			require.NoError(t, err)
			require.Len(t, feedAfter, len(feedBefore), "and no audit entry")

			// Refused whole: a status that leaves it finished, sent with the
			// reclassification, is not saved first. (A status that moves it out
			// of Resolved is a reopen, and the reclassification then applies.)
			finished, err := h.ticketStore.GetStatusByName(ctx, ticket.StatusNameClosed)
			require.NoError(t, err)
			statusBefore := got.StatusID
			res := h.do(t, http.MethodPatch, path, map[string]any{"category_id": other.ID.String(), "status_id": finished.ID})
			res.Body.Close()
			require.Equal(t, http.StatusConflict, res.StatusCode)
			got, err = h.ticketSvc.GetByID(ctx, tk.ID)
			require.NoError(t, err)
			require.Equal(t, statusBefore, got.StatusID, "the status in the same request is not saved")

			// No classification field: as today.
			res = h.do(t, http.MethodPatch, path, map[string]any{"assignee_user_id": h.staffID.String()})
			res.Body.Close()
			require.Equal(t, http.StatusOK, res.StatusCode)
		})
	}

	t.Run("an open ticket in another open status is still reclassified", func(t *testing.T) {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
			Subject: "Working", CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
		})
		require.NoError(t, err)
		inProgress, err := h.ticketStore.GetStatusByName(ctx, "In Progress")
		require.NoError(t, err)
		_, err = h.ticketSvc.UpdateStatus(ctx, tk.ID, inProgress.ID, staff)
		require.NoError(t, err)

		res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"category_id": other.ID.String()})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	})
}
