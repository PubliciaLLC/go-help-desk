package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #442: a public reply made through MCP add_reply used to pass an empty
// recipient, so the requester was never mailed. DESIGN.md says a public reply
// mails the requester, and the REST reply does. These tests drive both doors
// over equivalent tickets and compare what each one queues, so the two cannot
// drift apart again.

// replyEvent returns the single event of the given type a door queued.
func replyEvent(t *testing.T, h *harness, typ notification.EventType) notification.Event {
	t.Helper()
	var got []notification.Event
	for _, ev := range h.dispatcher.queued {
		if ev.Type == typ {
			got = append(got, ev)
		}
	}
	require.Len(t, got, 1, "expected exactly one %s event", typ)
	return got[0]
}

// A public reply queues ticket.replied; an internal note queues
// ticket.note_added (#469's split).
func eventFor(internal bool) notification.EventType {
	if internal {
		return notification.EventTicketNoteAdded
	}
	return notification.EventTicketReplied
}

func replyViaREST(t *testing.T, h *harness, id uuid.UUID, internal bool) notification.Event {
	t.Helper()
	h.dispatcher.queued = nil
	res := h.do(t, http.MethodPost, "/api/v1/tickets/"+id.String()+"/replies",
		map[string]any{"body": "we are on it", "internal": internal})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	return replyEvent(t, h, eventFor(internal))
}

func replyViaMCP(t *testing.T, h *harness, id uuid.UUID, internal bool) notification.Event {
	t.Helper()
	h.dispatcher.queued = nil
	c := openMCP(t, h, h.apiKey)
	defer c.closeBody()
	got := c.call(t, "add_reply", map[string]any{"ticket_id": id.String(), "body": "we are on it", "internal": internal})
	require.NotContains(t, got, `"isError":true`, got)
	return replyEvent(t, h, eventFor(internal))
}

// What matters to the requester's mail: who it goes to, whether it carries a
// guest link, and whether it is a note. Ids and timestamps differ per call.
type queuedMail struct {
	Recipient string
	GuestLink bool
	Internal  bool
}

func mailOf(ev notification.Event) queuedMail {
	internal, _ := ev.Payload["internal"].(bool)
	return queuedMail{Recipient: ev.Recipient, GuestLink: ev.GuestLink, Internal: internal}
}

func TestReplyRecipient_MCPQueuesWhatRESTQueues(t *testing.T) {
	ctx := context.Background()
	newTicket := func(t *testing.T, h *harness, in ticket.CreateInput) ticket.Ticket {
		t.Helper()
		in.Subject, in.Description, in.CategoryID = "Printer jammed", "again", h.catID
		tk, err := h.ticketSvc.Create(ctx, in)
		require.NoError(t, err)
		return tk
	}

	cases := []struct {
		name     string
		seed     func(t *testing.T, h *harness) ticket.Ticket
		after    func(t *testing.T, h *harness) // runs once both tickets exist
		wantTo   string
		wantLink bool
	}{
		{
			name: "guest ticket mails the guest, with a link",
			seed: func(t *testing.T, h *harness) ticket.Ticket {
				email := "guest@test.local"
				return newTicket(t, h, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb, GuestEmail: &email, GuestName: "Ada"})
			},
			wantTo: "guest@test.local", wantLink: true,
		},
		{
			name: "account ticket mails the account's address, no link",
			seed: func(t *testing.T, h *harness) ticket.Ticket {
				return newTicket(t, h, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb, ReporterUserID: &h.userID})
			},
			wantTo: "user@test.local",
		},
		{
			name: "a requester whose account is gone cannot be mailed",
			seed: func(t *testing.T, h *harness) ticket.Ticket {
				return newTicket(t, h, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb, ReporterUserID: &h.userID})
			},
			after: func(t *testing.T, h *harness) {
				require.NoError(t, h.userSvc.SoftDelete(ctx, h.userID))
			},
			wantTo: "",
		},
		{
			// Changed by Erik's decision: REST used to mail a staff member or
			// administrator who replied on a ticket they filed. They no longer
			// are; an ordinary requester still is (next test).
			name: "staff replying on a ticket they filed is not mailed about it",
			seed: func(t *testing.T, h *harness) ticket.Ticket {
				return newTicket(t, h, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb, ReporterUserID: &h.staffID})
			},
			wantTo: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// One harness: it is a single open transaction, so a second one
			// would wait on this one's locks forever.
			h, done := newHarness(t)
			defer done()
			h.dispatcher.record = true
			restTicket, mcpTicket := tc.seed(t, h), tc.seed(t, h)
			if tc.after != nil {
				tc.after(t, h)
			}
			viaREST := mailOf(replyViaREST(t, h, restTicket.ID, false))
			viaMCP := mailOf(replyViaMCP(t, h, mcpTicket.ID, false))

			require.Equal(t, tc.wantTo, viaREST.Recipient, "REST recipient")
			require.Equal(t, tc.wantLink, viaREST.GuestLink, "REST guest link")
			require.Equal(t, viaREST, viaMCP, "MCP must queue what REST queues")
		})
	}
}

// Erik's narrowing: only staff and admins skip the copy of their own reply. An
// ordinary requester replying on their own ticket is mailed, as before. (MCP
// add_reply is staff-only, so this door is REST only.)
func TestReplyRecipient_ARequesterRepliesOnTheirOwnTicketAndIsStillMailed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.dispatcher.record = true
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Printer jammed", Description: "again", CategoryID: h.catID, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	h.dispatcher.queued = nil
	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies",
		map[string]any{"body": "still broken"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, "user@test.local", replyEvent(t, h, notification.EventTicketReplied).Recipient)
}

// An administrator is held to the same rule as staff.
func TestReplyRecipient_AnAdminReplyingOnTheirOwnTicketIsNotMailed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.dispatcher.record = true
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Printer jammed", Description: "again", CategoryID: h.catID, ReporterUserID: &h.adminID,
	})
	require.NoError(t, err)

	h.dispatcher.queued = nil
	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies",
		map[string]any{"body": "noted"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Empty(t, replyEvent(t, h, notification.EventTicketReplied).Recipient)
}

func TestReplyRecipient_AnMCPInternalNoteMailsNobody(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.dispatcher.record = true
	email := "guest@test.local"
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Printer jammed", Description: "again", CategoryID: h.catID, GuestEmail: &email, GuestName: "Ada",
	})
	require.NoError(t, err)

	ev := replyViaMCP(t, h, tk.ID, true)
	require.Empty(t, ev.Recipient)
	require.False(t, ev.GuestLink, "an internal note must not mint a guest link")
	require.Equal(t, true, ev.Payload["internal"])
	require.Empty(t, ev.Payload["reporter_email"])
}
