package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Staff mail on a reopen (#464), over every way a ticket is reopened, through
// the real fan-out, outbox and worker. The fan-out's own rules are pinned in
// notify/staff_reopen_test.go; nothing proved that each door actually raises
// the event the fan-out keys on, so a door could stop announcing its reopen
// and every test would stay green.
//
// The assignee is the staff user and the person reopening is never them, who
// would be told nothing about their own act.

// reopenMail runs the worker and returns who was sent a reopen mail, and
// forgets it. A reopen that is also a reply is one mail, of the reopen kind.
func (m *staffMail) reopenMail(t *testing.T) []string {
	t.Helper()
	for {
		n, err := m.worker.RunOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}
	var out []string
	for _, ev := range m.sent.got {
		if ev.Type == notification.EventStaffReopened {
			out = append(out, ev.Recipient)
		}
	}
	m.sent.got = nil
	return out
}

// resolvedAssigned is a ticket held by the staff user and Resolved by the admin.
func resolvedAssigned(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	tk := assignedTicket(t, h)
	_, err := h.ticketSvc.Resolve(context.Background(), tk.ID, "done", ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin})
	require.NoError(t, err)
	return tk
}

func TestStaffReopenEmail_ARequestersReplyOnAResolvedTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := resolvedAssigned(t, h)
	m := wireStaffMail(t, h)

	res := h.doAsUser(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), map[string]any{"body": "still broken"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	require.Equal(t, []string{"staff@test.local"}, m.reopenMail(t))
}

func TestStaffReopenEmail_AGuestsReplyOnAResolvedTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h)
	adm := ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}
	_, err := h.ticketSvc.Assign(context.Background(), tk.ID, &h.staffID, nil, adm)
	require.NoError(t, err)
	_, err = h.ticketSvc.Resolve(context.Background(), tk.ID, "done", adm)
	require.NoError(t, err)
	// Resolving mailed the guest a fresh link, which retired the one seeded
	// above (a link is issued when the mail is sent).
	token, err = h.ticketSvc.IssueGuestToken(context.Background(), tk.ID)
	require.NoError(t, err)
	m := wireStaffMail(t, h)

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", token, map[string]any{"body": "still broken"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	require.Equal(t, []string{"staff@test.local"}, m.reopenMail(t))
}

func TestStaffReopenEmail_AStatusChangeOutOfResolved(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := resolvedAssigned(t, h)
	m := wireStaffMail(t, h)
	inProgress, err := h.ticketStore.GetStatusByName(context.Background(), "In Progress")
	require.NoError(t, err)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"status_id": inProgress.ID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	require.Equal(t, []string{"staff@test.local"}, m.reopenMail(t))
}

func TestStaffReopenEmail_AForceReopenOfAClosedTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetString(context.Background(), admin.KeyClosedReopenPolicy, "admin"))
	tk := assignedTicket(t, h)
	require.NoError(t, h.ticketSvc.Close(context.Background(), tk.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
	m := wireStaffMail(t, h)

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/reopen", nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	require.Equal(t, []string{"staff@test.local"}, m.reopenMail(t))
}

func TestStaffReopenEmail_AStatusChangeOverMCP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := resolvedAssigned(t, h)
	m := wireStaffMail(t, h)
	inProgress, err := h.ticketStore.GetStatusByName(context.Background(), "In Progress")
	require.NoError(t, err)

	c := openMCP(t, h, h.adminKey)
	defer c.closeBody()
	got := c.call(t, "update_ticket_status", map[string]any{"ticket_id": tk.ID.String(), "status_id": inProgress.ID.String()})
	require.NotContains(t, got, `"isError":true`, got)

	require.Equal(t, []string{"staff@test.local"}, m.reopenMail(t))
}
