package server_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/outboxstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
)

// Staff email (#441), end to end through the real fan-out, outbox and worker,
// ending at a recording stand-in for the SMTP sender. What is asserted is who
// would have been mailed.

type staffMail struct {
	h      *harness
	worker *notify.Worker
	sent   *recordingChannel
}

func wireStaffMail(t *testing.T, h *harness) *staffMail {
	t.Helper()
	store := outboxstore.New(h.q)
	queue := notify.NewOutboxDispatcher(store, []string{"email"}, nil)
	h.dispatcher.outbox = notify.NewStaffFanout(queue, h.userSvc, h.groupSvc, h.adminSvc.StaffEmailOverrides,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	sent := &recordingChannel{}
	w := notify.NewWorker(store, map[string]notification.Dispatcher{
		"email": notify.NewStaffMailDispatcher(sent, h.userSvc),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &staffMail{h: h, worker: w, sent: sent}
}

// recipients runs the worker and returns the addresses staff mail went to, in
// order, forgetting them afterwards.
func (m *staffMail) recipients(t *testing.T) []string {
	t.Helper()
	for {
		n, err := m.worker.RunOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}
	// Only staff mail: the requester's own mail travels the same channel and
	// is not what is under test.
	var out []string
	for _, ev := range m.sent.got {
		if ev.Type == notification.EventStaffAssigned || ev.Type == notification.EventStaffRequesterReplied {
			out = append(out, ev.Recipient)
		}
	}
	m.sent.got = nil
	return out
}

func newStaffTicket(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Printer", Description: "jammed", CategoryID: h.catID, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	return tk
}

func setPrefs(t *testing.T, h *harness, p user.EmailPreferencesPatch) {
	t.Helper()
	require.NoError(t, h.userSvc.SetEmailPreferences(context.Background(), h.staffID, p))
}

func flag(b bool) *bool { return &b }

// ── assignment ───────────────────────────────────────────────────────────────

func TestStaffEmail_AssigningATicketMailsTheAssignee(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	m := wireStaffMail(t, h)
	tk := newStaffTicket(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_user_id": h.staffID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))
}

func TestStaffEmail_NobodyIsMailedAboutTheirOwnAssignment(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	m := wireStaffMail(t, h)
	tk := newStaffTicket(t, h)

	// Staff assign the ticket to themselves.
	res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_user_id": h.staffID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	require.Empty(t, m.recipients(t))
}

func TestStaffEmail_AssignedToMeSwitch(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	m := wireStaffMail(t, h)
	tk := newStaffTicket(t, h)
	setPrefs(t, h, user.EmailPreferencesPatch{Assigned: flag(false)})

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_user_id": h.staffID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	require.Empty(t, m.recipients(t))
}

// Assigning to a group mails its members under their group switch, and
// someone who is both the assignee and a member gets one email.
func TestStaffEmail_GroupSwitchAndDedupe(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	m := wireStaffMail(t, h)
	g, err := h.groupSvc.Create(ctx, "Desk", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.staffID))
	tk := newStaffTicket(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_group_id": g.ID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t), "a group member is mailed")

	setPrefs(t, h, user.EmailPreferencesPatch{Group: flag(false)})
	tk2 := newStaffTicket(t, h)
	res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk2.ID.String(), map[string]any{"assignee_group_id": g.ID})
	res.Body.Close()
	require.Empty(t, m.recipients(t), "the group switch is off")

	// The assignee who is also in the group: one email, from whichever switch is on.
	setPrefs(t, h, user.EmailPreferencesPatch{Group: flag(true), Assigned: flag(true)})
	tk3 := newStaffTicket(t, h)
	res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk3.ID.String(),
		map[string]any{"assignee_user_id": h.staffID, "assignee_group_id": g.ID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t), "one email per person")
}

func TestStaffEmail_AdminOverride(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	m := wireStaffMail(t, h)
	assignTo := func() {
		tk := newStaffTicket(t, h)
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_user_id": h.staffID})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	}

	// Forced on for someone who turned it off.
	setPrefs(t, h, user.EmailPreferencesPatch{Assigned: flag(false)})
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyStaffEmailAssignedOverride, "on"))
	assignTo()
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))

	// Forced off for someone who left it on.
	setPrefs(t, h, user.EmailPreferencesPatch{Assigned: flag(true)})
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyStaffEmailAssignedOverride, "off"))
	assignTo()
	require.Empty(t, m.recipients(t))

	// Back to the person's own choice.
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyStaffEmailAssignedOverride, "user"))
	assignTo()
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))
}

// An account disabled after the mail was queued is not sent it.
func TestStaffEmail_ADisabledAccountIsNotMailed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	m := wireStaffMail(t, h)
	tk := newStaffTicket(t, h)
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_user_id": h.staffID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	require.NoError(t, h.userSvc.Disable(context.Background(), h.staffID))

	require.Empty(t, m.recipients(t))
}

// ── requester replies ────────────────────────────────────────────────────────

func assignedTicket(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	tk := newStaffTicket(t, h)
	_, err := h.ticketSvc.Assign(context.Background(), tk.ID, &h.staffID, nil, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin})
	require.NoError(t, err)
	return tk
}

func TestStaffEmail_ARequesterReplyMailsTheAssignee(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := assignedTicket(t, h)
	m := wireStaffMail(t, h) // after the assignment, which is not what is under test

	res := h.doAsUser(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), map[string]any{"body": "still broken"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))
}

func TestStaffEmail_ActivitySwitchAndOverride(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := assignedTicket(t, h)
	m := wireStaffMail(t, h)
	reply := func() {
		res := h.doAsUser(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), map[string]any{"body": "hello"})
		res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode)
	}

	setPrefs(t, h, user.EmailPreferencesPatch{Activity: flag(false)})
	reply()
	require.Empty(t, m.recipients(t), "the activity switch is off")

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyStaffEmailActivityOverride, "on"))
	reply()
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t), "forced on")

	setPrefs(t, h, user.EmailPreferencesPatch{Activity: flag(true)})
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyStaffEmailActivityOverride, "off"))
	reply()
	require.Empty(t, m.recipients(t), "forced off")
}

// A guest has no account and no actor; their reply still reaches staff.
func TestStaffEmail_AGuestReplyMailsTheAssignee(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h)
	_, err := h.ticketSvc.Assign(context.Background(), tk.ID, &h.staffID, nil, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin})
	require.NoError(t, err)
	m := wireStaffMail(t, h)

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", token, map[string]any{"body": "any news?"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))
}

// Staff talking to the requester, or to each other, is not a reason to mail
// staff.
func TestStaffEmail_StaffRepliesAndInternalNotesMailNobody(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := assignedTicket(t, h)
	m := wireStaffMail(t, h)

	for _, body := range []map[string]any{
		{"body": "on it"},
		{"body": "note to self", "internal": true},
	} {
		res := h.doAsAdmin(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), body)
		res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode)
	}
	require.Empty(t, m.recipients(t))
}
