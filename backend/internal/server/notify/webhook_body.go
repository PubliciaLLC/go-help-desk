package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// rawPayloadKeys are the only Payload keys the raw format carries: ids and
// flags. It is an allow list on purpose. A key added to an event tomorrow is
// kept off the wire until someone decides a receiver should have it, where a
// block list would send it. A receiver that needs the ticket's content reads
// it through the API using TicketID.
//
//	new_status_id  ticket.status_changed
//	target_id      ticket.linked
//	link_type      ticket.linked (an enum, not text)
//	internal       ticket.replied / ticket.note_added (false / true)
var rawPayloadKeys = []string{"new_status_id", "target_id", "link_type", "internal"}

// idsOnly returns ev reduced to what the raw format carries, and to what the
// copy kept for Redeliver may hold: the event name, ids, flags and times. No
// address, subject, tracking number, status name or text, in the typed fields
// or in Payload. The input is not changed.
func idsOnly(ev notification.Event) notification.Event {
	out := notification.Event{
		Type: ev.Type, TicketID: ev.TicketID, ActorID: ev.ActorID, OccurredAt: ev.OccurredAt,
		TargetWebhook: ev.TargetWebhook, TargetURLHash: ev.TargetURLHash,
	}
	for _, k := range rawPayloadKeys {
		if v, ok := ev.Payload[k]; ok {
			if out.Payload == nil {
				out.Payload = map[string]any{}
			}
			out.Payload[k] = v
		}
	}
	return out
}

// rawBody is the body a raw (or empty-format) hook receives: idsOnly, as JSON.
// The Slack, Teams, Discord and JIRA renderers do not use it; they render from
// the full in-memory event (bodyFor), which is never sent as is.
func rawBody(ev notification.Event) ([]byte, error) {
	return json.Marshal(idsOnly(ev))
}

// rowFor is the event as it is stored in one hook's outbox row. Never an
// address (Recipient, GuestLink, and the two address keys in Payload). For a
// raw hook that is the whole story: nothing but ids, since that is all it will
// send. A chat or ITSM hook keeps the subject, tracking number, status name
// and reply text its message is built from, as the single webhook row did
// before, and the row is deleted once sent.
func rowFor(ev notification.Event, hook authstore.WebhookConfig) notification.Event {
	if f := Format(hook.PayloadFormat); f == "" || f == FormatRaw {
		return idsOnly(ev)
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

// urlHash fingerprints a hook's URL for the outbox, which must not hold the
// URL itself: it can carry a token.
func urlHash(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}
