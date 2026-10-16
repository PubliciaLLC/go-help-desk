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

// One change, one delivery per hook (#506, ruling 6). The event a hook is told
// of carries every kind of the change; the hook fires once if any of them is a
// kind it subscribes to, labelled with the first it subscribes to.

// reopenChange is the example in the design: an agent moves a Resolved ticket
// back to In Progress and assigns it, on its first reopen.
func reopenChange() notification.Event {
	ev := fixtureStatusChangedEvent()
	ev.Type = notification.EventTicketReopened
	ev.Kinds = []notification.EventType{
		notification.EventTicketReopened, notification.EventTicketStatusChanged,
		notification.EventTicketAssigned, notification.EventTicketPriorityChanged,
	}
	ev.Ticket = &notification.TicketState{Priority: "high", CategoryID: uuid.New()}
	return ev
}

func hookOn(url string, events ...string) authstore.WebhookConfig {
	h := hookFor(url)
	h.Events = events
	return h
}

func bodiesOf(rc *receiver) func() []string { return capture(rc) }

func TestOneDelivery_AWildcardHookGetsOneDeliveryPerChange(t *testing.T) {
	rc := newReceiver(t, 200)
	bodies := bodiesOf(rc)
	rig := newQueueRig(t, rc.srv.Client(), hookOn(rc.srv.URL, "*"))

	require.NoError(t, rig.front.Dispatch(context.Background(), reopenChange()))
	rig.drain(t)

	require.Equal(t, 1, rc.count(), "four kinds, one change, one delivery")
	require.Equal(t, "ticket.reopened", rc.header(0, "X-GHD-Event"), "a wildcard is told the first kind")
	require.Contains(t, bodies()[0], `"Type":"ticket.reopened"`)
}

func TestOneDelivery_AHookOnSeveralNamesGetsOneDelivery(t *testing.T) {
	rc := newReceiver(t, 200)
	rig := newQueueRig(t, rc.srv.Client(),
		hookOn(rc.srv.URL, "ticket.assigned", "ticket.status_changed", "ticket.priority_changed"))

	require.NoError(t, rig.front.Dispatch(context.Background(), reopenChange()))
	rig.drain(t)

	require.Equal(t, 1, rc.count())
	require.Equal(t, "ticket.status_changed", rc.header(0, "X-GHD-Event"),
		"the first kind, in the fixed order, that THIS hook subscribes to")
}

// A hook that subscribed to one name is untouched: the same name, and exactly
// the body it always had.
func TestOneDelivery_ASingleNameHookGetsExactlyTodaysBody(t *testing.T) {
	rc := newReceiver(t, 200)
	bodies := bodiesOf(rc)
	rig := newQueueRig(t, rc.srv.Client(), hookOn(rc.srv.URL, "ticket.status_changed"))
	ev := reopenChange()

	require.NoError(t, rig.front.Dispatch(context.Background(), ev))
	rig.drain(t)

	require.Equal(t, 1, rc.count())
	require.Equal(t, "ticket.status_changed", rc.header(0, "X-GHD-Event"))
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(bodies()[0]), &got))
	require.Equal(t, "ticket.status_changed", got["Type"])
	require.Equal(t, map[string]any{"new_status_id": ev.Payload["new_status_id"].(uuid.UUID).String()}, got["Payload"])
	require.ElementsMatch(t, []string{"Type", "TicketID", "ActorID", "Payload", "OccurredAt"}, keysOf(got),
		"no kinds, no changes, no ticket state, no tracking number on the wire")
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The two new names (ruling 3) can be subscribed to and carry no id.
func TestOneDelivery_TheNewNamesAreSubscribableAndCarryNoPayload(t *testing.T) {
	for _, name := range []notification.EventType{notification.EventTicketPriorityChanged, notification.EventTicketClassified} {
		t.Run(string(name), func(t *testing.T) {
			require.True(t, IsWebhookEvent(string(name)))
			rc := newReceiver(t, 200)
			bodies := bodiesOf(rc)
			rig := newQueueRig(t, rc.srv.Client(), hookOn(rc.srv.URL, string(name)))
			ev := reopenChange()
			ev.Kinds = append(ev.Kinds, name) // a change that also reclassified, say
			ev.Payload["target_id"] = uuid.New()

			require.NoError(t, rig.front.Dispatch(context.Background(), ev))
			rig.drain(t)

			require.Equal(t, 1, rc.count())
			require.Equal(t, string(name), rc.header(0, "X-GHD-Event"))
			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(bodies()[0]), &got))
			require.Nil(t, got["Payload"], "the receiver reads the ticket through the API by TicketID")
		})
	}
}

// A change none of whose kinds the hook lists is not delivered, and queues
// nothing for it.
func TestOneDelivery_NoMatchingKindNoDelivery(t *testing.T) {
	rc := newReceiver(t, 200)
	rig := newQueueRig(t, rc.srv.Client(), hookOn(rc.srv.URL, "ticket.linked"))

	require.NoError(t, rig.front.Dispatch(context.Background(), reopenChange()))
	rig.drain(t)

	require.Zero(t, rc.count())
	require.Empty(t, liveRows(rig.store))
}

// Retries run the same test against the subscription as it is then: a hook that
// dropped the name a delivery was queued under, but lists another kind of the
// change, is sent that one, and one that lists none of them is not sent.
func TestOneDelivery_ARetryRecomputesTheNameFromTheSubscriptionNow(t *testing.T) {
	rc := newReceiver(t, 503, 200)
	hook := hookOn(rc.srv.URL, "ticket.status_changed")
	rig := newQueueRig(t, rc.srv.Client(), hook)

	require.NoError(t, rig.front.Dispatch(context.Background(), reopenChange()))
	rig.store.makeDue()
	_, err := rig.worker.RunOnce(context.Background()) // fan-out
	require.NoError(t, err)
	rig.store.makeDue()
	_, err = rig.worker.RunOnce(context.Background()) // first attempt: 503
	require.NoError(t, err)
	require.Equal(t, 1, rc.count())
	require.Equal(t, "ticket.status_changed", rc.header(0, "X-GHD-Event"))

	rig.setHook(hook.ID, func(h *authstore.WebhookConfig) { h.Events = []string{"ticket.assigned"} })
	rig.drain(t)

	require.Equal(t, 2, rc.count())
	require.Equal(t, "ticket.assigned", rc.header(1, "X-GHD-Event"))
	require.Equal(t, rc.header(0, "X-GHD-Delivery"), rc.header(1, "X-GHD-Delivery"), "still one delivery")
	require.Equal(t, "ticket.assigned", rig.hooks.last().EventType, "the history keeps the name that was sent")

	// And a subscription that no longer matches anything ends the retries.
	rc2 := newReceiver(t, 503, 200)
	hook2 := hookOn(rc2.srv.URL, "ticket.status_changed")
	rig2 := newQueueRig(t, rc2.srv.Client(), hook2)
	require.NoError(t, rig2.front.Dispatch(context.Background(), reopenChange()))
	rig2.store.makeDue()
	_, _ = rig2.worker.RunOnce(context.Background())
	rig2.store.makeDue()
	_, _ = rig2.worker.RunOnce(context.Background())
	rig2.setHook(hook2.ID, func(h *authstore.WebhookConfig) { h.Events = []string{"ticket.linked"} })
	rig2.drain(t)
	require.Equal(t, 1, rc2.count(), "no kind of the change is subscribed any more")
}

// A row queued before #506 has no kinds. It is a one-kind change and is
// delivered as it always was, its ticket attributes read from the old key.
func TestOneDelivery_ARowInTheOldShapeIsDeliveredAsBefore(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := hookOn(rc.srv.URL, "ticket.created")
	hook.Filter.Priorities = []string{"low"}
	rig := newQueueRig(t, rc.srv.Client(), hook)
	cat := uuid.New()
	old := `{"type":"ticket.created","ticket_id":"` + fixtureTicketID.String() +
		`","occurred_at":"2026-01-01T00:00:00Z","attrs":{"category_id":"` + cat.String() + `","priority":"low"}}`
	require.NoError(t, rig.store.Enqueue(context.Background(), uuid.New(), "webhook", []byte(old)))

	rig.drain(t)

	require.Equal(t, 1, rc.count())
	require.Equal(t, "ticket.created", rc.header(0, "X-GHD-Event"))
}

// An old per-hook row, queued under the old shape with its target already set,
// is one delivery to that hook.
func TestOneDelivery_AnOldPerHookRowIsStillOneDelivery(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := hookOn(rc.srv.URL, "*")
	rig := newQueueRig(t, rc.srv.Client(), hook)
	old := `{"type":"ticket.replied","ticket_id":"` + fixtureTicketID.String() + `","occurred_at":"2026-01-01T00:00:00Z",` +
		`"webhook_id":"` + hook.ID.String() + `","webhook_url_sha256":"` + urlHash(hook.URL) + `"}`
	require.NoError(t, rig.store.Enqueue(context.Background(), uuid.New(), "webhook", []byte(old)))

	rig.drain(t)

	require.Equal(t, 1, rc.count())
}

// Chat and ITSM formats lead with the hook's name for the change and add one
// line naming the other kinds, never values.
func TestOneDelivery_ChatMessagesNameTheOtherKinds(t *testing.T) {
	rc := newReceiver(t, 200)
	bodies := bodiesOf(rc)
	h := hookOn(rc.srv.URL, "*")
	h.PayloadFormat = "slack"
	rig := newQueueRig(t, rc.srv.Client(), h)

	require.NoError(t, rig.front.Dispatch(context.Background(), reopenChange()))
	rig.drain(t)

	require.Equal(t, 1, rc.count())
	require.Contains(t, bodies()[0], "Reopened")
	require.Contains(t, bodies()[0], "Also: status changed, assigned, priority changed")
}

func TestAlsoLine(t *testing.T) {
	require.Equal(t, "", alsoLine(notification.Event{Type: notification.EventTicketReplied}), "a one-kind change has none")
	require.Equal(t, "Also: assigned", alsoLine(notification.Event{
		Type:  notification.EventTicketStatusChanged,
		Kinds: []notification.EventType{notification.EventTicketStatusChanged, notification.EventTicketAssigned}}))
	require.Equal(t, "Also: status changed", alsoLine(notification.Event{
		Type:  notification.EventTicketAssigned,
		Kinds: []notification.EventType{notification.EventTicketStatusChanged, notification.EventTicketAssigned}}),
		"the name the hook was told is not repeated")
}

// The "Also" line names only kinds the receiving hook is subscribed to: it must
// never tell a channel about a kind it did not ask for (an internal note in
// particular). Worked out per hook from its subscription at delivery time.
func assignAndNote() notification.Event {
	ev := fixtureStatusChangedEvent()
	ev.Type = notification.EventTicketAssigned
	ev.Kinds = []notification.EventType{
		notification.EventTicketAssigned, notification.EventTicketPriorityChanged, notification.EventTicketNoteAdded,
	}
	ev.Ticket = &notification.TicketState{Priority: "high", CategoryID: uuid.New()}
	return ev
}

func slackHook(url string, events ...string) authstore.WebhookConfig {
	h := hookOn(url, events...)
	h.PayloadFormat = "slack"
	return h
}

func deliverOne(t *testing.T, h authstore.WebhookConfig, rc *receiver, ev notification.Event) string {
	t.Helper()
	bodies := bodiesOf(rc)
	rig := newQueueRig(t, rc.srv.Client(), h)
	require.NoError(t, rig.front.Dispatch(context.Background(), ev))
	rig.drain(t)
	require.Equal(t, 1, rc.count())
	return bodies()[0]
}

func TestAlso_ASingleNameHookGetsNoAlsoLine(t *testing.T) {
	rc := newReceiver(t, 200)
	body := deliverOne(t, slackHook(rc.srv.URL, "ticket.assigned"), rc, assignAndNote())
	require.NotContains(t, body, "Also")
	require.NotContains(t, body, "note added")
}

func TestAlso_NamesOnlyWhatTheHookSubscribesTo(t *testing.T) {
	rc := newReceiver(t, 200)
	body := deliverOne(t, slackHook(rc.srv.URL, "ticket.assigned", "ticket.priority_changed"), rc, assignAndNote())
	require.Contains(t, body, "Also: priority changed")
	require.NotContains(t, body, "note added")
}

func TestAlso_AWildcardHookSeesEveryKind(t *testing.T) {
	rc := newReceiver(t, 200)
	body := deliverOne(t, slackHook(rc.srv.URL, "*"), rc, assignAndNote())
	require.Contains(t, body, "Also: priority changed, note added")
}

func TestAlso_JiraKeyFollowsTheSameRule(t *testing.T) {
	rc := newReceiver(t, 200)
	h := hookOn(rc.srv.URL, "ticket.assigned")
	h.PayloadFormat = "jira"
	require.NotContains(t, deliverOne(t, h, rc, assignAndNote()), `"also"`)
}

// A retry works the line out again from the subscription as it is then.
func TestAlso_ARetryAfterTheSubscriptionNarrowsDropsTheKind(t *testing.T) {
	rc := newReceiver(t, 503, 200)
	bodies := bodiesOf(rc)
	hook := slackHook(rc.srv.URL, "ticket.assigned", "ticket.note_added")
	rig := newQueueRig(t, rc.srv.Client(), hook)
	require.NoError(t, rig.front.Dispatch(context.Background(), assignAndNote()))
	rig.store.makeDue()
	_, err := rig.worker.RunOnce(context.Background()) // fan-out
	require.NoError(t, err)
	rig.store.makeDue()
	_, err = rig.worker.RunOnce(context.Background()) // 503
	require.NoError(t, err)
	require.Contains(t, bodies()[0], "Also: note added")

	rig.setHook(hook.ID, func(h *authstore.WebhookConfig) { h.Events = []string{"ticket.assigned"} })
	rig.drain(t)

	require.Len(t, bodies(), 2)
	require.NotContains(t, bodies()[1], "note added")
	require.NotContains(t, bodies()[1], "Also")
}

// Reply text rides on a chat or ITSM message only for a hook that subscribes to
// ticket.replied (or "*"). A hook on ticket.reopened alone still gets its
// Reopened message, with subject and link, but not the reply (CTO ruling 12;
// 1.2.x sent the reply text only to hooks that asked for replies). Internal
// notes are never included, whatever the subscription.
func reopenWithReply() notification.Event {
	ev := fixtureReplyEvent(false)
	ev.Type = notification.EventTicketReopened
	ev.Kinds = []notification.EventType{notification.EventTicketReopened, notification.EventTicketReplied}
	ev.Ticket = &notification.TicketState{Priority: "low", CategoryID: uuid.New()}
	return ev
}

func TestReplyText_OnlyForHooksThatSubscribeToReplies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []string
		want   bool
	}{
		{"reopened only", []string{"ticket.reopened"}, false},
		{"reopened and replied", []string{"ticket.reopened", "ticket.replied"}, true},
		{"wildcard", []string{"*"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := newReceiver(t, 200)
			body := deliverOne(t, slackHook(rc.srv.URL, tc.events...), rc, reopenWithReply())
			require.Contains(t, body, "Reopened")
			require.Contains(t, body, "Printer jammed", "the subject stays")
			if tc.want {
				require.Contains(t, body, "Paper tray 2 was empty.")
			} else {
				require.NotContains(t, body, "Paper tray 2 was empty.")
			}
		})
	}
}

func TestReplyText_AnInternalNoteIsNeverIncluded(t *testing.T) {
	rc := newReceiver(t, 200)
	ev := reopenWithReply()
	ev.Kinds = []notification.EventType{notification.EventTicketReopened, notification.EventTicketReplied, notification.EventTicketNoteAdded}
	ev.Payload["internal"] = true
	body := deliverOne(t, slackHook(rc.srv.URL, "*"), rc, ev)
	require.NotContains(t, body, "Paper tray 2 was empty.")
}
