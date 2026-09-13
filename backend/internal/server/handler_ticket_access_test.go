package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Visibility used to be enforced per handler, on GET /{id} and PATCH /{id}
// only. The fifteen routes beneath them had no check at all, so
// GET /{id}/replies returned the whole thread — staff-only internal notes
// included — to any signed-in user holding a ticket UUID, while GET /{id} on
// the same ticket correctly answered 403. Confirmed by execution before the
// fix: 403 on the ticket, 200 and the note body on its replies.
//
// The gate is now middleware on the /{id} subtree. These tests walk every route
// under it, so a route added later without thinking about access fails here
// rather than shipping.

func foreignTicket(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject:        "Exec compensation review",
		Description:    "Sensitive",
		CategoryID:     h.catID,
		Priority:       ticket.PriorityHigh,
		ReporterUserID: &h.adminID, // NOT the reporting user
	})
	require.NoError(t, err)
	return tk
}

func TestTicketSubtree_RefusesAnUnrelatedReportingUser(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk := foreignTicket(t, h)
	id := tk.ID.String()
	base := "/api/v1/tickets/" + id

	// Baseline: the ticket itself is refused, so any route that answers 2xx
	// below is reachable on a ticket the caller cannot read.
	res := h.doAsUser(t, http.MethodGet, base, nil)
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode, "precondition")

	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, base, nil},
		{http.MethodPatch, base, map[string]any{"clear_assignee": true}},
		{http.MethodGet, base + "/replies", nil},
		{http.MethodPost, base + "/replies", map[string]any{"body": "injected"}},
		{http.MethodPost, base + "/resolve", map[string]any{"resolution_notes": "x"}},
		{http.MethodPost, base + "/reopen", nil},
		{http.MethodPost, base + "/close", nil},
		{http.MethodGet, base + "/links", nil},
		{http.MethodPost, base + "/links", map[string]any{"target_ticket_id": uuid.New().String(), "link_type": "related"}},
		{http.MethodGet, base + "/history", nil},
		{http.MethodGet, base + "/tags", nil},
		{http.MethodPost, base + "/tags", map[string]any{"name": "vip"}},
		{http.MethodGet, base + "/attachments", nil},
		{http.MethodGet, base + "/custom-fields", nil},
		{http.MethodPut, base + "/custom-fields", map[string]any{"values": map[string]any{}}},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s %s", tc.method, strings.TrimPrefix(tc.path, base)), func(t *testing.T) {
			res := h.doAsUser(t, tc.method, tc.path, tc.body)
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode,
				"a ticket the caller cannot read must not be reachable here; body %s", b)
		})
	}
}

// Internal notes are staff-to-staff. Access to a ticket is not access to them:
// the reporter may read their own thread and must still not see them.
func TestListReplies_HidesInternalNotesFromTheReporter(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	own, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "My laptop will not boot", CategoryID: h.catID,
		Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	newStatus := statusIDNamed(t, h, ticket.StatusNameNew)
	staffActor := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}

	_, err = h.ticketSvc.AddReply(ctx, own.ID, "We are looking into it", false, true, "", staffActor, 7, newStatus)
	require.NoError(t, err)
	_, err = h.ticketSvc.AddReply(ctx, own.ID, "INTERNAL: replace under warranty, cost code 4471", true, false, "", staffActor, 7, newStatus)
	require.NoError(t, err)

	t.Run("the reporter sees only the public reply", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+own.ID.String()+"/replies", nil)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()

		require.Equal(t, http.StatusOK, res.StatusCode, "the reporter may read their own thread")
		require.Contains(t, string(b), "We are looking into it")
		require.NotContains(t, string(b), "cost code 4471",
			"an internal note must never reach the reporter")
	})

	t.Run("staff see the whole thread", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+own.ID.String()+"/replies", nil)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()

		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Contains(t, string(b), "cost code 4471")
	})
}

// The gate resolves tracking numbers as well as UUIDs; that path must be
// access-checked too, or it becomes the way around the gate.
func TestTicketSubtree_TrackingNumberIsAlsoGated(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk := foreignTicket(t, h)

	res := h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+string(tk.TrackingNumber), nil)
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)

	res = h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+string(tk.TrackingNumber)+"/replies", nil)
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode,
		"the tracking-number form must not bypass the gate")
}

func statusIDNamed(t *testing.T, h *harness, name string) uuid.UUID {
	t.Helper()
	sts, err := h.ticketSvc.ListStatuses(context.Background())
	require.NoError(t, err)
	for _, s := range sts {
		if s.Name == name {
			return s.ID
		}
	}
	t.Fatalf("status %q not found", name)
	return uuid.Nil
}
