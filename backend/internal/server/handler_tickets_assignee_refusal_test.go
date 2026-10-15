package server_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// "A refused request changes nothing" for an assignee or group the request
// names and the help desk will not accept (#468, Erik 2026-10-14). The status
// used to be saved first and the assignment refused after it, so the 400 came
// back over a ticket that had already moved and carried an audit row.
func TestUpdateTicket_RefusedAssignmentChangesNothing_HTTP(t *testing.T) {
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

	refusedAssignments := map[string]map[string]any{
		"a reporting user":                 {"assignee_user_id": h.userID},
		"an unknown user":                  {"assignee_user_id": uuid.New()},
		"an unknown group":                 {"assignee_group_id": uuid.New()},
		"a good user and an unknown group": {"assignee_user_id": h.staffID, "assignee_group_id": uuid.New()},
	}
	// What rides along with the refused assignment. Each of these used to be
	// saved before the 400, except the priority, which is saved last and was
	// only ever safe by accident of order.
	alongside := map[string]map[string]any{
		"status":                     {"status_id": inProgress.ID},
		"status and priority":        {"status_id": inProgress.ID, "priority": "high"},
		"priority":                   {"priority": "high"},
		"category":                   {"category_id": otherCat.ID},
		"status, category, priority": {"status_id": inProgress.ID, "category_id": otherCat.ID, "priority": "high"},
	}

	for an, assignment := range refusedAssignments {
		for rn, rest := range alongside {
			t.Run(an+" with "+rn, func(t *testing.T) {
				tk := mk()
				before, err := h.ticketSvc.ListAuditFeed(ctx, tk.ID, true)
				require.NoError(t, err)

				body := map[string]any{}
				for k, v := range assignment {
					body[k] = v
				}
				for k, v := range rest {
					body[k] = v
				}
				res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), body)
				b, _ := io.ReadAll(res.Body)
				res.Body.Close()
				require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", b)

				got, err := h.ticketSvc.GetByID(ctx, tk.ID)
				require.NoError(t, err)
				require.Equal(t, tk.StatusID, got.StatusID, "status")
				require.Equal(t, tk.AssigneeUserID, got.AssigneeUserID, "assignee")
				require.Equal(t, tk.AssigneeGroupID, got.AssigneeGroupID, "group")
				require.Equal(t, tk.CategoryID, got.CategoryID, "category")
				require.Equal(t, ticket.PriorityMedium, got.Priority, "priority")
				after, err := h.ticketSvc.ListAuditFeed(ctx, tk.ID, true)
				require.NoError(t, err)
				require.Len(t, after, len(before), "a refused request writes no audit row")
			})
		}
	}

	t.Run("staff: a good assignment with a status and a priority still applies", func(t *testing.T) {
		tk := mk()
		grp, err := h.groupSvc.Create(ctx, "Helpers", "")
		require.NoError(t, err)
		res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{
			"status_id": inProgress.ID, "assignee_user_id": h.staffID, "assignee_group_id": grp.ID, "priority": "high",
		})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		require.Equal(t, inProgress.ID, got.StatusID)
		require.Equal(t, h.staffID, *got.AssigneeUserID)
		require.Equal(t, grp.ID, *got.AssigneeGroupID)
		require.Equal(t, ticket.PriorityHigh, got.Priority)
	})
}
