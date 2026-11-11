package notify

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Writes that announced nothing now do (#630). These tests cover what the
// webhook side owes the four new names and the one new key on replies and
// notes: what goes on the wire, who is sent it, and that a redelivery and the
// chat renderers cope.

// g4Event is a change of one kind with the Payload keys every kind of the
// pre-tag events might have added, so a body test also proves a key goes out
// only under the name it belongs to.
func g4Event(kind notification.EventType, payload map[string]any) notification.Event {
	ev := contentEvent()
	ev.Type, ev.Kinds = kind, []notification.EventType{kind}
	ev.Payload = payload
	return ev
}

func TestRawBody_G4Names(t *testing.T) {
	target, attachment, person, reply := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tests := []struct {
		name    notification.EventType
		payload map[string]any
		want    map[string]any
	}{
		{notification.EventTicketReplied,
			map[string]any{"reply_id": reply, "internal": false, "ReplyBody": "SECRET", "reporter_email": "a@b.c"},
			map[string]any{"reply_id": reply.String()}},
		{notification.EventTicketNoteAdded,
			map[string]any{"reply_id": reply, "internal": true},
			map[string]any{"reply_id": reply.String()}},
		{notification.EventTicketUnlinked,
			map[string]any{"target_id": target, "link_type": "duplicate_of"},
			map[string]any{"target_id": target.String(), "link_type": "duplicate_of"}},
		{notification.EventTicketAttachmentAdded,
			map[string]any{"attachment_id": attachment, "Filename": "SECRET.pdf"},
			map[string]any{"attachment_id": attachment.String()}},
		{notification.EventTicketFollowed,
			map[string]any{"user_id": person, "display_name": "SECRET NAME"},
			map[string]any{"user_id": person.String()}},
		{notification.EventTicketUnfollowed,
			map[string]any{"user_id": person},
			map[string]any{"user_id": person.String()}},
	}
	for _, tc := range tests {
		t.Run(string(tc.name), func(t *testing.T) {
			body, err := rawBody(g4Event(tc.name, tc.payload))
			require.NoError(t, err)
			var got struct{ Payload map[string]any }
			require.NoError(t, json.Unmarshal(body, &got))
			require.Equal(t, tc.want, got.Payload, "exactly the ids this name owns, nothing else")
			require.NotContains(t, string(body), "SECRET")
		})
	}
}

// A key belongs to its name: a change that carries a reply and a reopen sends a
// hook told ticket.reopened no reply id, as before.
func TestRawBody_ReplyIDOnlyUnderTheReplyNames(t *testing.T) {
	ev := g4Event(notification.EventTicketReopened, map[string]any{"reply_id": uuid.New(), "new_status_id": uuid.New()})
	body, err := rawBody(ev)
	require.NoError(t, err)
	require.NotContains(t, string(body), "reply_id")
}

// D40: "*" and a legacy empty list do not get follow events; naming them does.
func TestSubscribedKinds_FollowEventsAreOptInByName(t *testing.T) {
	followed := g4Event(notification.EventTicketFollowed, map[string]any{"user_id": uuid.New()})
	attachment := g4Event(notification.EventTicketAttachmentAdded, map[string]any{"attachment_id": uuid.New()})
	tests := []struct {
		name   string
		events []string
		ev     notification.Event
		want   bool
	}{
		{"star, followed", []string{"*"}, followed, false},
		{"empty list, followed", nil, followed, false},
		{"named, followed", []string{"ticket.followed"}, followed, true},
		{"star plus the name, followed", []string{"*", "ticket.followed"}, followed, true},
		{"the other opt-in name does not cover it", []string{"ticket.unfollowed"}, followed, false},
		{"star, attachment_added is an ordinary event", []string{"*"}, attachment, true},
		{"empty list, attachment_added", nil, attachment, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := subscribedKind(hookOn("http://x", tc.events...), tc.ev)
			require.Equal(t, tc.want, ok)
		})
	}
}

// Delivered end to end through the queue: three hooks, one follow.
func TestOneDelivery_AFollowReachesOnlyTheHookThatNamesIt(t *testing.T) {
	star, legacy, named := newReceiver(t, 200), newReceiver(t, 200), newReceiver(t, 200)
	rig := newQueueRig(t, star.srv.Client(),
		hookOn(star.srv.URL, "*"), hookOn(legacy.srv.URL), hookOn(named.srv.URL, "ticket.followed"))
	bodies := capture(named)
	person := uuid.New()

	require.NoError(t, rig.front.Dispatch(context.Background(),
		g4Event(notification.EventTicketFollowed, map[string]any{"user_id": person})))
	rig.drain(t)

	require.Zero(t, star.count(), "all-events hooks do not get follows")
	require.Zero(t, legacy.count())
	require.Equal(t, 1, named.count())
	require.Equal(t, "ticket.followed", named.header(0, "X-GHD-Event"))
	require.Contains(t, bodies()[0], `"user_id":"`+person.String()+`"`)
}

// A reassignment that also drops the old assignee's follow is one change. A hook
// on "*" is told ticket.assigned and learns nothing of the unfollow; one that
// names ticket.unfollowed gets it.
func TestOneDelivery_AnUnfollowRidingAReassignmentStaysOptIn(t *testing.T) {
	ev := g4Event(notification.EventTicketAssigned, map[string]any{"user_id": uuid.New()})
	ev.Kinds = []notification.EventType{notification.EventTicketAssigned, notification.EventTicketUnfollowed}

	got, ok := forHook(hookOn("http://x", "*"), ev)
	require.True(t, ok)
	require.Equal(t, notification.EventTicketAssigned, got.Type)
	require.Equal(t, []notification.EventType{notification.EventTicketAssigned}, got.Kinds,
		"the chat Also line is built from Kinds, so it cannot name the unfollow")
	body, err := rawBody(got)
	require.NoError(t, err)
	require.NotContains(t, string(body), "user_id")

	got, ok = forHook(hookOn("http://x", "ticket.unfollowed"), ev)
	require.True(t, ok)
	require.Equal(t, notification.EventTicketUnfollowed, got.Type)
}

// The history keeps the ids a replay needs, and a replay is held to the
// subscription as it is now: a hook that stopped naming the event is refused.
func TestRedeliver_G4NamesKeepTheirIDsAndOptInIsRechecked(t *testing.T) {
	for _, tc := range []struct {
		name    notification.EventType
		payload map[string]any
		key     string
	}{
		{notification.EventTicketUnlinked, map[string]any{"target_id": uuid.New(), "link_type": "related_to"}, "link_type"},
		{notification.EventTicketAttachmentAdded, map[string]any{"attachment_id": uuid.New()}, "attachment_id"},
		{notification.EventTicketFollowed, map[string]any{"user_id": uuid.New()}, "user_id"},
		{notification.EventTicketUnfollowed, map[string]any{"user_id": uuid.New()}, "user_id"},
		{notification.EventTicketReplied, map[string]any{"reply_id": uuid.New(), "internal": false}, "reply_id"},
		{notification.EventTicketNoteAdded, map[string]any{"reply_id": uuid.New(), "internal": true}, "reply_id"},
	} {
		t.Run(string(tc.name), func(t *testing.T) {
			ev := g4Event(tc.name, tc.payload)
			rc := newReceiver(t, 200)
			bodies := capture(rc)
			hook := hookOn(rc.srv.URL, string(tc.name))
			rig := newQueueRig(t, rc.srv.Client(), hook)
			require.NoError(t, rig.front.Dispatch(context.Background(), ev))
			rig.drain(t)
			require.Equal(t, 1, rc.count())
			first := rig.hooks.last()
			require.Equal(t, string(tc.name), first.EventType, "the history view shows the new name")

			require.NoError(t, rig.disp.Redeliver(context.Background(), hook.ID, first.DeliveryID))
			rig.drain(t)
			require.Equal(t, 2, rc.count())
			require.Equal(t, bodyWithoutTimes(t, bodies()[0]), bodyWithoutTimes(t, bodies()[1]),
				"a replay carries the same ids as the original")
			require.Contains(t, bodies()[1], tc.key)

			rig.setHook(hook.ID, func(h *authstore.WebhookConfig) { h.Events = []string{"ticket.status_changed"} })
			require.ErrorIs(t, rig.disp.Redeliver(context.Background(), hook.ID, first.DeliveryID), ErrWebhookNotSubscribed)

			// "*" covers every name but the follow events.
			rig.setHook(hook.ID, func(h *authstore.WebhookConfig) { h.Events = []string{"*"} })
			err := rig.disp.Redeliver(context.Background(), hook.ID, first.DeliveryID)
			if notification.IsOptInWebhookEvent(tc.name) {
				require.ErrorIs(t, err, ErrWebhookNotSubscribed)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func bodyWithoutTimes(t *testing.T, b string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(b), &m))
	delete(m, "OccurredAt")
	return m
}

func TestHeadline_G4Names(t *testing.T) {
	target := uuid.New()
	tests := []struct {
		ev   notification.Event
		want string
	}{
		{g4Event(notification.EventTicketUnlinked, map[string]any{"target_id": target, "link_type": "related_to"}),
			"Unlinked from " + target.String() + " (related_to)"},
		{g4Event(notification.EventTicketAttachmentAdded, map[string]any{"attachment_id": uuid.New()}), "Attachment added"},
		// The person is an id, and a chat message does not resolve one.
		{g4Event(notification.EventTicketFollowed, map[string]any{"user_id": uuid.New()}), "Ticket followed"},
		{g4Event(notification.EventTicketUnfollowed, map[string]any{"user_id": uuid.New()}), "Ticket unfollowed"},
	}
	for _, tc := range tests {
		t.Run(string(tc.ev.Type), func(t *testing.T) {
			s := summarize(tc.ev, fixtureBaseURL)
			require.Equal(t, tc.want, s.Headline)
			require.NotContains(t, s.Headline, "SECRET")
		})
	}
	// "Also" names the new kinds too, for a hook that does subscribe to them.
	ev := g4Event(notification.EventTicketAssigned, nil)
	ev.Kinds = []notification.EventType{notification.EventTicketAssigned, notification.EventTicketUnfollowed}
	require.Equal(t, "Also: unfollowed", alsoLine(ev))
}

// D41: a change flagged SuppressStaffMail makes the fan-out hand on no staff
// mail, even when the ticket still has an assignee, a group and followers.
func TestStaffFanout_SuppressedChangeMailsNobody(t *testing.T) {
	a := staffUser("assignee")
	member := staffUser("member")
	group := uuid.New()
	ev := assignedEvent(nil, &a.ID, &group)
	ev.SuppressStaffMail = true
	next := &recorder{}
	f := NewStaffFanout(next, newFakeStaffUsers(a, member), fakeStaffGroups{group: {member.ID}},
		fakeStaffFollowers{ev.TicketID: {a.ID}}, noOverrides, quietLog())

	require.NoError(t, f.Dispatch(context.Background(), ev))
	require.Empty(t, next.got)

	ev.SuppressStaffMail = false
	require.NoError(t, f.Dispatch(context.Background(), ev))
	require.NotEmpty(t, next.got, "the same change without the flag does mail: the test is not vacuous")
}
