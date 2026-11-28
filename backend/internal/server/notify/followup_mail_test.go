package notify

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// #625: a guest whose closed ticket was followed up is told so, in words that
// fit, and not "We have received your ticket" for a ticket they did not file.

func followUpEvent(mod func(*notification.Event)) func(*EmailDispatcher) error {
	return goldenEvent(notification.EventTicketCreated, func(e *notification.Event) {
		e.FollowUp = true
		if mod != nil {
			mod(e)
		}
	})
}

func TestFollowUpMail_SaysItIsAFollowUpAndCarriesTheNewTicketAndLink(t *testing.T) {
	guest := func(e *notification.Event) { e.GuestToken = "guest-token-abc" }
	subj, body := parts(t, captureMessage(t, followUpEvent(guest), nil))

	require.Equal(t, "We have opened a follow-up ticket [GHD-2026-000042]", subj)
	require.Contains(t, body, "We have opened a new ticket to follow up on your earlier one.")
	require.Contains(t, body, "Ticket: GHD-2026-000042")
	require.Contains(t, body, "https://help.example.com/g#guest-token-abc", "a working link to the new ticket")
	require.NotContains(t, subj+body, "We have received")
}

func TestFollowUpMail_NoTrackingNumberStillReads(t *testing.T) {
	subj, _ := parts(t, captureMessage(t, followUpEvent(func(e *notification.Event) { e.TrackingNumber = "" }), nil))
	require.Equal(t, "We have opened a follow-up ticket", subj)
}

// The wording follows the event, and only that: an ordinary created mail is
// still the received mail.
func TestFollowUpMail_OnlyAFollowUpGetsIt(t *testing.T) {
	subj, body := parts(t, captureMessage(t, createdEvent, nil))
	require.Equal(t, "We have received [GHD-2026-000042]", subj)
	require.Contains(t, body, "We have received your ticket.")
}

// The flag is read from the queued row, which is what the worker sends from.
func TestFollowUpMail_SurvivesTheOutboxRow(t *testing.T) {
	send := func(d *EmailDispatcher) error {
		ev := notification.Event{Type: notification.EventTicketCreated, TicketID: goldenTicketID,
			TrackingNumber: "GHD-2026-000042", Recipient: "to@example.com", FollowUp: true}
		row, err := notification.EncodeEvent(ev)
		require.NoError(t, err)
		got, err := notification.DecodeEvent(row)
		require.NoError(t, err)
		return d.Dispatch(t.Context(), got)
	}
	subj, _ := parts(t, captureMessage(t, send, nil))
	require.Equal(t, "We have opened a follow-up ticket [GHD-2026-000042]", subj)
}

// The follow-up mail is a template of its own, so an operator who rewrote the
// received mail keeps their text there and edits this one separately.
func TestFollowUpMail_HasItsOwnOverride(t *testing.T) {
	ov := overrides(
		emailtemplate.Override{Name: "ticket_created", Subject: "Received", Body: "{{.TicketURL}}"},
		emailtemplate.Override{Name: "ticket_follow_up_created", Subject: "Follow-up {{.TrackingNumber}}", Body: "Follow-up at {{.TicketURL}}"},
	)
	subj, body := parts(t, captureMessage(t, followUpEvent(nil), with(ov)))
	require.Equal(t, "Follow-up GHD-2026-000042", subj)
	require.Equal(t, "Follow-up at https://help.example.com/tickets/"+goldenTicketID.String()+"\n", body)

	subj, _ = parts(t, captureMessage(t, createdEvent, with(ov)))
	require.Equal(t, "Received", subj)
}
