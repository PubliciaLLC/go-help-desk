package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// rawKeysFor gives the only Payload keys the raw format carries, by name: ids. It is
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

// historyKeys are the Payload keys the copy kept for Redeliver holds: those
// the raw body sends for the name the hook is told and for the opt-in kinds it
// subscribed to (the keys of rawBody), and nothing more. What is stored for a
// hook is what that hook may receive, so a hook that did not opt into
// ticket.unfollowed never has the person's id sitting in its history on a
// ticket.assigned delivery. A replay needs exactly these (the reply to look up
// again, the attachment, the person). ticket.linked adds link_type, which its
// raw body does not send but a replay's chat headline reads.
func historyKeys(ev notification.Event) []string {
	return keptKeys(append([]notification.EventType{ev.Type}, optInKinds(ev)...))
}

// keptKeys are the keys a stored copy holds for the names in kinds.
func keptKeys(kinds []notification.EventType) []string {
	var keys []string
	for _, k := range kinds {
		keys = append(keys, rawKeysFor(k)...)
		if k == notification.EventTicketLinked {
			keys = append(keys, "link_type")
		}
	}
	return keys
}

// optInKinds are the opt-in kinds an event carries besides the name it is sent
// under.
func optInKinds(ev notification.Event) []notification.EventType {
	var out []notification.EventType
	for _, k := range ev.Kinds {
		if k != ev.Type && notification.IsOptInWebhookEvent(k) {
			out = append(out, k)
		}
	}
	return out
}

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
// has finally failed: ids only, plus historyKeys. No Attrs: a replay
// reads the ticket's attributes from the ticket as it is now.
func historyEvent(ev notification.Event) notification.Event {
	out := keepOnly(ev, historyKeys(ev))
	// The opt-in kinds the delivery carried, so a replay is sent the id they add
	// to the body (rawBody). Kinds is otherwise left out: a replay is the name
	// that was sent, and a hook that is no longer opted in drops these again.
	if optIn := optInKinds(ev); len(optIn) > 0 {
		out.Kinds = append([]notification.EventType{ev.Type}, optIn...)
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
	for _, k := range optInKinds(ev) {
		keys = append(keys, rawKeysFor(k)...)
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
// is built from them (rawBody and the renderers never read them as content).
// The copy kept in the history and the scrubbed failed row go through
// historyEvent, which drops the ticket state and keeps only the opt-in kinds.
//
// The name is worked out again at every attempt, so an edit to the hook's events
// while a delivery is retrying can change it. A raw row therefore holds the ids
// of every ordinary kind of the change, as it did before opt-in kinds existed.
// An opt-in kind's id is the exception, and it is held only when the hook named
// that kind when the row was queued. Opt-in counts from when the hook named the
// event: a change that happened before is never upgraded by a later edit, so a
// retry then sends the new name without the person's id.
func rowFor(ev notification.Event, hook authstore.WebhookConfig) notification.Event {
	if isRaw(hook) {
		var kinds []notification.EventType
		for _, k := range ev.AllKinds() {
			if !notification.IsOptInWebhookEvent(k) || slices.Contains(hook.Events, string(k)) {
				kinds = append(kinds, k)
			}
		}
		out := keepOnly(ev, keptKeys(kinds))
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
