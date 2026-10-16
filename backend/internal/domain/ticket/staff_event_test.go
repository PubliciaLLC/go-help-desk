package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

func firstEvent(t *testing.T, events []notification.Event, typ notification.EventType) notification.Event {
	t.Helper()
	for _, e := range events {
		if e.Type == typ {
			return e
		}
	}
	t.Fatalf("no %s event dispatched", typ)
	return notification.Event{}
}

// Staff mail is addressed from the event, so the event has to say who now
// holds the ticket (#441).
func TestAssign_EventNamesTheAssignee(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	actor := uuid.New()
	assignee, group := uuid.New(), uuid.New()

	_, err := h.svc.Assign(context.Background(), seeded.ID, &assignee, &group,
		ticket.Actor{UserID: &actor, Role: user.RoleStaff})
	require.NoError(t, err)

	ev := firstEvent(t, h.dispatcher.events, notification.EventTicketAssigned)
	require.Equal(t, &assignee, ev.Ticket.AssigneeUserID)
	require.Equal(t, &group, ev.Ticket.AssigneeGroupID)
	require.Equal(t, &actor, ev.ActorID)
}

// Unassigning dispatches the same event with nobody on it, so the fan-out has
// nobody to mail rather than a stale assignee.
func TestAssign_UnassigningLeavesTheEventEmpty(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	actor := uuid.New()

	_, err := h.svc.Assign(context.Background(), seeded.ID, nil, nil,
		ticket.Actor{UserID: &actor, Role: user.RoleStaff})
	require.NoError(t, err)

	ev := firstEvent(t, h.dispatcher.events, notification.EventTicketAssigned)
	require.Nil(t, ev.Ticket.AssigneeUserID)
	require.Nil(t, ev.Ticket.AssigneeGroupID)
}

// The requester is whoever filed the ticket, not whoever has the user role
// (#441): a staff member who files a ticket and then replies on it is the
// requester replying, and a staff member replying on somebody else's is not.
func TestReply_FromRequesterFlag(t *testing.T) {
	ctx := context.Background()
	staffID := uuid.New()
	reporter := uuid.New()
	cases := []struct {
		name  string
		reply func(h *harness) error
		want  bool
	}{
		{"an account holder's public reply", func(h *harness) error {
			tk := h.seedOpenTicket(reporter)
			_, err := h.svc.AddReply(ctx, tk.ID, "hi", false, false, "", ticket.Actor{UserID: &reporter, Role: user.RoleUser}, 30, h.newStatus.ID)
			return err
		}, true},
		{"a guest's reply", func(h *harness) error {
			tk, _ := guestTicket(t, h)
			_, err := h.svc.AddGuestReply(ctx, tk.ID, "hi", 30, h.newStatus.ID)
			return err
		}, true},
		{"a staff member replying on a ticket they filed", func(h *harness) error {
			tk := h.seedOpenTicket(staffID)
			_, err := h.svc.AddReply(ctx, tk.ID, "hi", false, false, "", ticket.Actor{UserID: &staffID, Role: user.RoleStaff}, 30, h.newStatus.ID)
			return err
		}, true},
		{"an administrator replying on a ticket they filed", func(h *harness) error {
			tk := h.seedOpenTicket(staffID)
			_, err := h.svc.AddReply(ctx, tk.ID, "hi", false, false, "", ticket.Actor{UserID: &staffID, Role: user.RoleAdmin}, 30, h.newStatus.ID)
			return err
		}, true},
		{"a staff reply on somebody else's ticket", func(h *harness) error {
			tk := h.seedOpen()
			_, err := h.svc.AddReply(ctx, tk.ID, "hi", false, false, "", ticket.Actor{UserID: &staffID, Role: user.RoleStaff}, 30, h.newStatus.ID)
			return err
		}, false},
		{"a staff internal note on somebody else's ticket", func(h *harness) error {
			tk := h.seedOpen()
			_, err := h.svc.AddReply(ctx, tk.ID, "hi", true, false, "", ticket.Actor{UserID: &staffID, Role: user.RoleStaff}, 30, h.newStatus.ID)
			return err
		}, false},
		{"an internal note by the staff member who filed the ticket", func(h *harness) error {
			tk := h.seedOpenTicket(staffID)
			_, err := h.svc.AddReply(ctx, tk.ID, "hi", true, false, "", ticket.Actor{UserID: &staffID, Role: user.RoleStaff}, 30, h.newStatus.ID)
			return err
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			require.NoError(t, tc.reply(h))
			// Any event, not only ticket.replied: an internal note may be its
			// own event type (#469), and must still never be a requester's.
			got := false
			for _, ev := range h.dispatcher.events {
				got = got || ev.FromRequester
			}
			require.Equal(t, tc.want, got)
		})
	}
}

// The reply event also says who holds the ticket, so a requester's reply can
// be routed without reading the ticket again.
func TestReply_EventNamesTheAssignee(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	tk := h.seedOpenTicket(reporter)
	assignee, group := uuid.New(), uuid.New()
	tk.AssigneeUserID, tk.AssigneeGroupID = &assignee, &group
	h.store.seed(tk)

	_, err := h.svc.AddReply(context.Background(), tk.ID, "hi", false, false, "",
		ticket.Actor{UserID: &reporter, Role: user.RoleUser}, 30, h.newStatus.ID)
	require.NoError(t, err)

	ev := firstEvent(t, h.dispatcher.events, notification.EventTicketReplied)
	require.Equal(t, &assignee, ev.Ticket.AssigneeUserID)
	require.Equal(t, &group, ev.Ticket.AssigneeGroupID)
}
