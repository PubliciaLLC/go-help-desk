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
//	target_id      ticket.linked
//
// Whether an event is a reply or a note is the event name, so no flag is sent.
var rawPayloadKeys = []string{"new_status_id", "target_id"}

// historyPayloadKeys are what the copy kept for Redeliver adds: the link type
// (an enum, for the headline) and the id of the reply the event announced, so a
// redelivery can look the reply up again. Neither is sent in a raw body.
var historyPayloadKeys = append(append([]string(nil), rawPayloadKeys...), "link_type", "reply_id")

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

// historyEvent is the event as it is kept for Redeliver and in a raw hook's
// row: ids only, plus historyPayloadKeys.
func historyEvent(ev notification.Event) notification.Event {
	return keepOnly(ev, historyPayloadKeys)
}

// rawBody is the body a raw (or empty-format) hook receives: ids only, as JSON.
// The Slack, Teams, Discord and JIRA renderers do not use it; they render from
// the full in-memory event (bodyFor), which is never sent as is.
func rawBody(ev notification.Event) ([]byte, error) {
	return json.Marshal(keepOnly(ev, rawPayloadKeys))
}

// rowFor is the event as it is stored in one hook's outbox row. Never an
// address (Recipient, GuestLink, and the two address keys in Payload). For a
// raw hook that is the whole story: nothing but ids, since that is all it will
// send. A chat or ITSM hook keeps the subject, tracking number, status name
// and reply text its message is built from until the row is sent and deleted,
// or scrubbed if the delivery finally fails (scrubFailedRow).
func rowFor(ev notification.Event, hook authstore.WebhookConfig) notification.Event {
	if isRaw(hook) {
		return historyEvent(ev)
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
