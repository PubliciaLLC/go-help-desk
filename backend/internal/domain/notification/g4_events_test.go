package notification

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The four names #630 adds are webhook events, and they are in KindOrder after
// the kinds that existed, so a change that carries one still leads with the
// kind a subscriber already knew.
func TestNewEventNames_AreWebhookEventsAndOrdered(t *testing.T) {
	names := []EventType{EventTicketUnlinked, EventTicketAttachmentAdded, EventTicketFollowed, EventTicketUnfollowed}
	require.Equal(t, []string{"ticket.unlinked", "ticket.attachment_added", "ticket.followed", "ticket.unfollowed"},
		[]string{string(names[0]), string(names[1]), string(names[2]), string(names[3])})
	for _, n := range names {
		require.True(t, IsWebhookEvent(string(n)), "%s must be a name a hook can list", n)
		require.Contains(t, WebhookEvents, n)
		require.Contains(t, KindOrder, n)
	}
	require.Equal(t, names, KindOrder[len(KindOrder)-4:])
}

// D40: following is personal, so only a hook that names it gets it.
func TestOptInWebhookEvents(t *testing.T) {
	require.True(t, IsOptInWebhookEvent(EventTicketFollowed))
	require.True(t, IsOptInWebhookEvent(EventTicketUnfollowed))
	for _, k := range KindOrder {
		if k == EventTicketFollowed || k == EventTicketUnfollowed {
			continue
		}
		require.False(t, IsOptInWebhookEvent(k), "%s goes to every hook that lists *", k)
	}
	// Opt-in names are still valid subscriptions.
	for _, k := range OptInWebhookEvents {
		require.Contains(t, WebhookEvents, k)
	}
}

// A bulk unassignment is announced but never mails staff (D41): the mail would
// tell the people of a deleted group "assigned to you" in a burst.
func TestEvent_SuppressStaffMail(t *testing.T) {
	ev := Event{Type: EventTicketAssigned, Kinds: []EventType{EventTicketAssigned}}
	require.True(t, ev.MailsStaff(), "an ordinary assignment may mail staff")
	require.True(t, Carries("email", ev))

	ev.SuppressStaffMail = true
	require.False(t, ev.MailsStaff())
	require.False(t, Carries("email", ev), "no email row at all")
	require.True(t, Carries("webhook", ev), "the webhook still hears of it")
}

// The flag survives the outbox, and survives a merge: a piece that says "no
// mail" is not undone by a later piece that does not.
func TestEvent_SuppressStaffMail_RoundTripsAndMerges(t *testing.T) {
	tid := uuid.New()
	c := &Changes{}
	c.Raise(Event{Kinds: []EventType{EventTicketAssigned}, TicketID: tid, SuppressStaffMail: true})
	c.Raise(Event{Kinds: []EventType{EventTicketUnfollowed}, TicketID: tid})
	evs := c.Events()
	require.Len(t, evs, 1)
	require.True(t, evs[0].SuppressStaffMail)

	b, err := EncodeEvent(evs[0])
	require.NoError(t, err)
	got, err := DecodeEvent(b)
	require.NoError(t, err)
	require.True(t, got.SuppressStaffMail)
}
