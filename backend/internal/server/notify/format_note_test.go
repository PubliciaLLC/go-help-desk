package notify

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// ticket.note_added is a staff-only note (#448). Every format says so, and
// none carries a body even when a caller wrongly put one in the payload: the
// summary decides what a chat message may hold, not the payload.
func TestRenderNoteAdded_NeverCarriesABody(t *testing.T) {
	ev := fixtureReplyEvent(true)
	ev.Type = notification.EventTicketNoteAdded
	ev.Payload = map[string]any{"ReplyBody": "INTERNAL: cost code 4471"} // no "internal" flag on purpose

	for name, render := range map[string]renderer{
		"slack": renderSlack, "teams": renderTeams, "discord": renderDiscord, "jira": renderJIRA,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := render(summarize(ev, fixtureBaseURL))
			require.NoError(t, err)
			if name != "jira" { // JIRA has no headline; it says "internal":true, below
				require.Contains(t, string(got), "Internal note added")
			}
			require.NotContains(t, string(got), "cost code 4471")
		})
	}
}

func TestRenderJIRA_NoteAddedIsFlaggedInternal(t *testing.T) {
	ev := fixtureReplyEvent(true)
	ev.Type = notification.EventTicketNoteAdded

	got, err := renderJIRA(summarize(ev, fixtureBaseURL))
	require.NoError(t, err)
	require.Contains(t, string(got), `"internal":true`)
	require.Contains(t, string(got), `"event":"ticket.note_added"`)
	require.NotContains(t, string(got), `"body"`)
}

func TestIsWebhookEvent_NoteAdded(t *testing.T) {
	require.True(t, IsWebhookEvent("ticket.note_added"))
	note := notification.Event{Type: notification.EventTicketNoteAdded}
	_, ok := subscribedKind(authstore.WebhookConfig{Events: []string{"ticket.note_added"}}, note)
	require.True(t, ok)
	_, ok = subscribedKind(authstore.WebhookConfig{Events: []string{"ticket.replied"}}, note)
	require.False(t, ok, "subscribing to replies must not subscribe to notes")
}
