package ticket_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The webhook dispatcher marshals the whole event and POSTs it to every
// subscriber. An internal note's body in the payload meant a credential holding
// only webhooks:write — refused tickets:read — could register a URL and receive
// every staff-only note on the instance. Scopes are supposed to narrow.
//
// Asserted on the marshalled bytes, not the map: what reaches a subscriber is
// the JSON, and a key excluded from the map but reachable some other way would
// still be a leak.
func TestReplyEvent_InternalNoteBodyNeverLeavesTheServer(t *testing.T) {
	const secret = "INTERNAL: the customer is under investigation"

	for _, tc := range []struct {
		name     string
		internal bool
		wantBody bool
	}{
		{name: "public reply carries its body", internal: false, wantBody: true},
		{name: "internal note does not", internal: true, wantBody: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := replyEventForTest(t, secret, tc.internal)

			raw, err := json.Marshal(ev)
			require.NoError(t, err)

			if tc.wantBody {
				require.Contains(t, string(raw), secret)
				return
			}
			require.NotContains(t, string(raw), secret,
				"a staff-only note must not reach a webhook subscriber")
			require.False(t, strings.Contains(strings.ToLower(string(raw)), "replybody"))
		})
	}
}

// A subscriber must be able to tell the two apart. Before this it could not,
// which is why the body looked safe to include.
func TestReplyEvent_CarriesTheInternalFlag(t *testing.T) {
	for _, internal := range []bool{true, false} {
		ev := replyEventForTest(t, "body", internal)
		require.Equal(t, internal, ev.Payload["internal"],
			"the event must say whether the reply was internal")
	}
}

// replyEventForTest builds the event AddReply dispatches, via the real service.
func replyEventForTest(t *testing.T, body string, internal bool) notification.Event {
	t.Helper()
	h := newHarness(t)
	seeded := h.seedOpen()

	agent := uuid.New()
	_, err := h.svc.AddReply(context.Background(), seeded.ID, body, internal, false,
		"reporter@example.com",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff},
		30, h.newStatus.ID)
	require.NoError(t, err)

	for _, e := range h.dispatcher.events {
		if e.Type == notification.EventTicketReplied {
			return e
		}
	}
	t.Fatal("no ticket.replied event dispatched")
	return notification.Event{}
}
