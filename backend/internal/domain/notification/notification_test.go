package notification

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A webhook subscriber receives this struct, marshalled whole. Two of its
// fields must never appear in that: Recipient is a customer's email address,
// and both it and TrackingNumber exist only so the mail dispatcher can read
// them without touching Payload.
//
// Adding a field to this struct is the easy way to hand every webhook
// subscriber something they were not supposed to see, which has happened here
// before — an internal note's body reached subscribers through Payload. This
// test is the check on the wire shape.
func TestEvent_RecipientAndTrackingNumberStayOutOfTheWebhookPayload(t *testing.T) {
	ev := Event{
		Type:           EventTicketReplied,
		TicketID:       uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		OccurredAt:     time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
		Payload:        map[string]any{"ReplyBody": "the reply"},
		TrackingNumber: "GHD-2026-000001",
		Recipient:      "customer@example.com",
	}

	raw, err := json.Marshal(ev)
	require.NoError(t, err)
	body := string(raw)

	require.NotContains(t, body, "customer@example.com",
		"a subscriber must not be handed the customer's address")
	require.NotContains(t, body, "Recipient")
	require.NotContains(t, body, "TrackingNumber\":",
		"the field must not be serialised; the payload key of the same name may be")
	require.NotContains(t, body, "GHD-2026-000001")

	// What subscribers do get is unchanged.
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "ticket.replied", got["Type"])
	require.Equal(t, "11111111-2222-3333-4444-555555555555", got["TicketID"])
	require.Equal(t, map[string]any{"ReplyBody": "the reply"}, got["Payload"])
}

// Noop is what a deployment with every channel disabled runs, so it has to
// accept anything without complaint.
func TestNoop_AcceptsEveryEventType(t *testing.T) {
	for _, ty := range []EventType{
		EventTicketCreated, EventTicketAssigned, EventTicketStatusChanged,
		EventTicketReplied, EventTicketResolved, EventTicketClosed,
		EventTicketReopened, EventTicketLinked,
	} {
		require.NoError(t, Noop{}.Dispatch(t.Context(), Event{Type: ty}))
	}
}
