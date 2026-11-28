package server_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// #444: MCP create_ticket with reporter_user_id records the caller as the actor,
// as REST does (CreateInput.FiledBy), and the reporter as the ticket's reporter.
// Without the argument the caller is both, as before.
func TestMCPCreateTicket_RecordsTheCallerAsTheActorWhenFilingForSomeoneElse(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.dispatcher.record = true
	writer := h.newServiceClient(t, "Nagios", "tickets:read", "tickets:write")
	sess := newMCPSession(t, h, writer.Token)

	createdTicket := func(args map[string]any) uuid.UUID {
		t.Helper()
		args["subject"], args["description"], args["category_id"] = "Raised over MCP", "x", h.catID.String()
		text, isErr := sess.tool("create_ticket", args)
		require.False(t, isErr, "create_ticket: %s", text)
		for _, ev := range h.dispatcher.queued {
			if ev.Type == notification.EventTicketCreated {
				return ev.TicketID
			}
		}
		require.FailNow(t, "no created event")
		return uuid.Nil
	}

	t.Run("for another user", func(t *testing.T) {
		h.dispatcher.queued = nil
		id := createdTicket(map[string]any{"reporter_user_id": h.userID.String()})

		stored, err := h.ticketSvc.GetByID(t.Context(), id)
		require.NoError(t, err)
		require.NotNil(t, stored.ReporterUserID)
		require.Equal(t, h.userID, *stored.ReporterUserID, "the reporter is who it is for")
		actor := createdAuditActor(t, h, id)
		require.NotNil(t, actor)
		require.Equal(t, writer.UserID, *actor, "the audit entry names the caller")
		for _, ev := range h.dispatcher.queued {
			if ev.Type == notification.EventTicketCreated {
				require.NotNil(t, ev.ActorID)
				require.Equal(t, writer.UserID, *ev.ActorID, "and so does the event")
			}
		}
	})

	t.Run("for no one else, by omission", func(t *testing.T) {
		h.dispatcher.queued = nil
		id := createdTicket(map[string]any{})

		actor := createdAuditActor(t, h, id)
		require.NotNil(t, actor)
		require.Equal(t, writer.UserID, *actor)
		for _, ev := range h.dispatcher.queued {
			if ev.Type == notification.EventTicketCreated {
				require.Empty(t, ev.Recipient, "nobody is mailed about their own ticket")
			}
		}
	})

	t.Run("for no one else, by naming themselves", func(t *testing.T) {
		h.dispatcher.queued = nil
		id := createdTicket(map[string]any{"reporter_user_id": writer.UserID.String()})

		actor := createdAuditActor(t, h, id)
		require.NotNil(t, actor)
		require.Equal(t, writer.UserID, *actor)
	})
}
