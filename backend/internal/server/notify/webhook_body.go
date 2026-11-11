package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// rawPayloadKeys are the only Payload keys the raw format carries: ids. It is
// an allow list on purpose. A key added to an event tomorrow is kept off the
// wire until someone decides a receiver should have it, where a block list
// would send it. A receiver that needs the ticket's content reads it through
// the API using TicketID.
//
//	new_status_id  ticket.status_changed
//	target_id      ticket.linked, ticket.unlinked
//	link_type      ticket.unlinked (an enum; the same two ends can be linked in
//	               more than one way, so the id alone does not say which went)
//	reply_id       ticket.replied, ticket.note_added
//	attachment_id  ticket.attachment_added
//	user_id        ticket.followed, ticket.unfollowed (an id, never a name)
//
// Whether an event is a reply or a note is the event name, so no flag is sent.
//
// An event now announces a whole change, and its Payload holds the keys of
// every kind it carried (#506). The body must still be exactly what the name
// it is sent under always carried, so a key goes out only for the name it
// belongs to (rawKeysFor): a hook told ticket.resolved gets no
// new_status_id, a hook told ticket.priority_changed or ticket.classified gets
// no Payload at all, and the receiver reads the ticket through the API.
var rawPayloadKeys = []string{"new_status_id", "target_id", "link_type", "reply_id", "attachment_id", "user_id"}

// rawKeysFor is the allow-listed Payload keys that belong to an event name.
func rawKeysFor(t notification.EventType) []string {
	switch t {
	case notification.EventTicketStatusChanged:
		return []string{"new_status_id"}
	case notification.EventTicketLinked:
		return []string{"target_id"}
	case notification.EventTicketUnlinked:
		return []string{"target_id", "link_type"}
	case notification.EventTicketReplied, notification.EventTicketNoteAdded:
		return []string{"reply_id"}
	case notification.EventTicketAttachmentAdded:
		return []string{"attachment_id"}
	case notification.EventTicketFollowed, notification.EventTicketUnfollowed:
		return []string{"user_id"}
	}
	return nil
}

// historyPayloadKeys are what the copy kept for Redeliver holds: every key any
// raw body carries. It is the same slice as rawPayloadKeys on purpose, not a
// leftover: the history must keep each id some raw body can send, or a replay
// of that body would lose it. A key added to rawKeysFor goes in rawPayloadKeys
// and so reaches the history with it. A replay needs them (the reply to look up again, the
// attachment, the person), and the history view shows only the event name. The
// link type is in the list for ticket.unlinked, and a ticket.linked replay
// needs it for the chat headline although its raw body does not send it.
var historyPayloadKeys = rawPayloadKeys

// keepOnly returns ev reduced to the event name, ids and times, and of Payload
// only the given keys. No address, subject, tracking number, status name or
// text, in the typed fields or in Payload. The input is not changed.
func keepOnly(ev notification.Event, keys []string) notification.Event {
	out := notification.Event{
		Type: ev.Type, TicketID: ev.TicketID, ActorID: ev.ActorID, OccurredAt: ev.OccurredAt,
		TargetWebhook: ev.TargetWebhook, TargetURLHash: ev.TargetURLHash,
	}
	for _, k := range keys {
		if v, ok := ev.Payload[k]; ok {
			if out.Payload == nil {
				out.Payload = map[string]any{}
			}
			out.Payload[k] = v
		}
	}
	return out
}

// historyEvent is the event as it is kept for Redeliver and after a delivery
// has finally failed: ids only, plus historyPayloadKeys. No Attrs: a replay
// reads the ticket's attributes from the ticket as it is now.
func historyEvent(ev notification.Event) notification.Event {
	out := keepOnly(ev, historyPayloadKeys)
	// The opt-in kinds the delivery carried, so a replay is sent the id they add
	// to the body (rawBody). Kinds is otherwise left out: a replay is the name
	// that was sent, and a hook that is no longer opted in drops these again.
	for _, k := range ev.Kinds {
		if k != ev.Type && notification.IsOptInWebhookEvent(k) {
			out.Kinds = append(out.Kinds, k)
		}
	}
	if len(out.Kinds) > 0 {
		out.Kinds = append([]notification.EventType{ev.Type}, out.Kinds...)
	}
	return out
}

// rawBody is the body a raw (or empty-format) hook receives: ids only, as JSON.
// The Slack, Teams, Discord and JIRA renderers do not use it; they render from
// the full in-memory event (bodyFor), which is never sent as is.
//
// The keys are those of the name the hook is told, plus those of any opt-in
// kind the hook subscribed to that the change also carries: a reassignment that
// dropped a follow is sent as ticket.assigned, and a hook that listed
// ticket.unfollowed must still learn who unfollowed. ev.Kinds is already cut to
// what this hook subscribes to (forHook), so a hook that did not opt in has no
// such kind and gets nothing extra. Only opt-in kinds do this: every other kind
// keeps to its own name, which is what pins the bodies of the older events.
func rawBody(ev notification.Event) ([]byte, error) {
	keys := rawKeysFor(ev.Type)
	for _, k := range ev.Kinds {
		if k != ev.Type && notification.IsOptInWebhookEvent(k) {
			keys = append(keys, rawKeysFor(k)...)
		}
	}
	return json.Marshal(keepOnly(ev, keys))
}

// rowFor is the event as it is stored in one hook's outbox row. Never an
// address (Recipient, GuestLink, and the two address keys in Payload). For a
// raw hook that is the whole story: nothing but ids, since that is all it will
// send. A chat or ITSM hook keeps the subject, tracking number, status name
// and reply text its message is built from until the row is sent and deleted,
// or scrubbed if the delivery finally fails (scrubFailedRow).
//
// Every row of either kind keeps the kinds and the ticket state: ids, enums and
// a priority, for the next attempt, which works out the name to send and judges
// the hook's filter as they are then (#413, #506). They are internal. No body
// is built from them (rawBody and the renderers never read them as content),
// and the copy kept in the history and the scrubbed failed row go through
// historyEvent, which drops them.
func rowFor(ev notification.Event, hook authstore.WebhookConfig) notification.Event {
	if isRaw(hook) {
		out := historyEvent(ev)
		out.Kinds, out.Ticket = ev.Kinds, ev.Ticket
		// forHook reads it on the next attempt: a routed assignment names no actor.
		out.RoutedAssignment = ev.RoutedAssignment
		return out
	}
	out := ev
	out.Recipient, out.GuestLink = "", false
	if ev.Payload != nil {
		out.Payload = maps.Clone(ev.Payload)
		delete(out.Payload, "guest_email")
		delete(out.Payload, "reporter_email")
	}
	return out
}

func isRaw(hook authstore.WebhookConfig) bool {
	f := Format(hook.PayloadFormat)
	return f == "" || f == FormatRaw
}

// urlHash fingerprints a hook's URL for the outbox, which must not hold the
// URL itself: it can carry a token.
func urlHash(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}
