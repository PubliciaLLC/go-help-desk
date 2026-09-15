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

// The email dispatcher is allowed to use exactly two values off an event: the
// tracking number and the recipient. Both have to come off the persisted
// ticket, not out of Payload, and not out of the struct the service built from
// the request.
//
// The distinction looks academic because the two are normally the same string.
// It is the whole of the fix for go/email-injection: an email built from
// request text is a message this server sends, from its own domain, saying
// whatever the person who filed the ticket chose. Payload still carries that
// text — webhook subscribers want it — so the only thing keeping it out of the
// mail is that these two fields are sourced somewhere else.
//
// onRead makes the difference visible: the store hands back a different value
// than it was given, so a field populated from the request fails here.

func TestCreate_AnnouncesTheStoredTicketNotTheRequestedOne(t *testing.T) {
	h := newHarness(t)
	requested := "requested@example.com"

	h.store.onRead = func(tk ticket.Ticket) ticket.Ticket {
		stored := "stored@example.com"
		tk.GuestEmail = &stored
		tk.TrackingNumber = "STORED-2026-000001"
		return tk
	}

	_, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject:    "Printer broken",
		GuestEmail: &requested,
		GuestName:  "Ada",
	})
	require.NoError(t, err)

	require.Len(t, h.dispatcher.events, 1)
	ev := h.dispatcher.events[0]
	require.Equal(t, notification.EventTicketCreated, ev.Type)
	require.Equal(t, "stored@example.com", ev.Recipient,
		"the address mailed must be the one on the row")
	require.Equal(t, "STORED-2026-000001", ev.TrackingNumber,
		"the tracking number mailed must be the one on the row")
}

// A read failure after the commit must cost the notification, not the ticket.
// The ticket exists either way; what must not happen is falling back to the
// request copy and mailing that.
func TestCreate_ReadFailureAfterCommitSendsNoEmail(t *testing.T) {
	h := newHarness(t)
	guest := "guest@example.com"

	h.store.errGetByID = errNotFound

	tk, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject:    "Printer broken",
		GuestEmail: &guest,
		GuestName:  "Ada",
	})
	require.NoError(t, err, "the ticket is committed; a read failure must not undo it")
	require.NotEqual(t, ticket.Ticket{}, tk)

	require.Len(t, h.dispatcher.events, 1)
	require.Empty(t, h.dispatcher.events[0].Recipient,
		"with no row to read, no address may be announced")
}

func TestAddReply_AnnouncesTheLockedRowsTrackingNumber(t *testing.T) {
	h := newHarness(t)
	tk := h.seedOpen()
	staff := uuid.New()

	h.store.onRead = func(got ticket.Ticket) ticket.Ticket {
		got.TrackingNumber = "STORED-2026-000002"
		return got
	}

	_, err := h.svc.AddReply(context.Background(), tk.ID, "a reply", false, true,
		"reporter@example.com", ticket.Actor{UserID: &staff, Role: user.RoleStaff}, 0, h.newStatus.ID)
	require.NoError(t, err)

	var replied *notification.Event
	for i := range h.dispatcher.events {
		if h.dispatcher.events[i].Type == notification.EventTicketReplied {
			replied = &h.dispatcher.events[i]
		}
	}
	require.NotNil(t, replied)
	require.Equal(t, "STORED-2026-000002", replied.TrackingNumber)
	require.Equal(t, "reporter@example.com", replied.Recipient)
}
