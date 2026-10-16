package notification

import (
	"maps"
	"slices"

	"github.com/google/uuid"
)

// One change, one event (#506). A transaction that changes a ticket raises
// what it did into a Changes collector; the collector merges everything raised
// for the same ticket into one Event, and the transaction's runner writes that
// event to the outbox before it commits. A rolled-back transaction therefore
// announces nothing, and a committed one cannot lose its announcement.

// KindOrder is the order Kinds are listed in: the order of importance. The
// first kind is the event's Type, and the label a hook that subscribes to
// several kinds is told.
var KindOrder = []EventType{
	EventTicketCreated,
	EventTicketReopened,
	EventTicketResolved,
	EventTicketClosed,
	EventTicketStatusChanged,
	EventTicketAssigned,
	EventTicketPriorityChanged,
	EventTicketClassified,
	EventTicketReplied,
	EventTicketNoteAdded,
	EventTicketLinked,
}

// Change is one field moving from one value to another. Ids and enums only,
// never text: an event is read by consumers that must not be handed content.
// A nil side is "none".
type Change struct {
	Field string  `json:"field"`
	From  *string `json:"from"`
	To    *string `json:"to"`
}

// IDChange is a change of a nullable id field.
func IDChange(field string, from, to *uuid.UUID) Change {
	str := func(id *uuid.UUID) *string {
		if id == nil {
			return nil
		}
		s := id.String()
		return &s
	}
	return Change{Field: field, From: str(from), To: str(to)}
}

// ValueChange is a change of an enum or id field that always has a value.
func ValueChange(field, from, to string) Change {
	return Change{Field: field, From: &from, To: &to}
}

// Changes collects what one transaction did, per ticket. The zero value is
// ready to use. Not safe for concurrent use: it belongs to one transaction.
type Changes struct {
	events []*Event
	byID   map[uuid.UUID]*Event
}

// Raise adds what one piece of a change did to ticket ev.TicketID. ev.Kinds
// names what happened (ev.Type is used when it is empty). Raising for a ticket
// that already has an event merges into it:
//
//   - kinds are a set, kept in KindOrder;
//   - changes are keyed by field, with the first "from" and the last "to";
//   - Ticket is the last one raised, so the final raise must describe the
//     ticket as the transaction leaves it;
//   - Payload keys are merged, a later key replacing an earlier one;
//   - the strings are the last non-empty one, the flags are true if any piece
//     said so, and the actor and time are the first piece's.
func (c *Changes) Raise(ev Event) {
	if len(ev.Kinds) == 0 && ev.Type != "" {
		ev.Kinds = []EventType{ev.Type}
	}
	dst, ok := c.byID[ev.TicketID]
	if !ok {
		if c.byID == nil {
			c.byID = map[uuid.UUID]*Event{}
		}
		first := ev
		first.Kinds = nil
		first.Changes = nil
		first.Payload = nil
		dst = &first
		c.byID[ev.TicketID] = dst
		c.events = append(c.events, dst)
	}

	dst.Kinds = sortKinds(append(dst.Kinds, ev.Kinds...))
	dst.Type = dst.Kinds[0]

	for _, ch := range ev.Changes {
		i := slices.IndexFunc(dst.Changes, func(have Change) bool { return have.Field == ch.Field })
		if i < 0 {
			dst.Changes = append(dst.Changes, ch)
			continue
		}
		dst.Changes[i].To = ch.To
	}

	if len(ev.Payload) > 0 {
		if dst.Payload == nil {
			dst.Payload = map[string]any{}
		}
		maps.Copy(dst.Payload, ev.Payload)
	}
	if ev.Ticket != nil {
		dst.Ticket = ev.Ticket
	}
	if dst.ActorID == nil {
		dst.ActorID = ev.ActorID
	}
	for _, s := range []struct {
		to   *string
		from string
	}{
		{&dst.TrackingNumber, ev.TrackingNumber}, {&dst.Recipient, ev.Recipient},
		{&dst.Subject, ev.Subject}, {&dst.StatusName, ev.StatusName},
	} {
		if s.from != "" {
			*s.to = s.from
		}
	}
	dst.GuestLink = dst.GuestLink || ev.GuestLink
	dst.FromRequester = dst.FromRequester || ev.FromRequester
}

// Events returns the merged events, one per ticket, in the order the tickets
// were first raised. Empty when nothing was raised, in which case nothing is
// written.
func (c *Changes) Events() []Event {
	if c == nil {
		return nil
	}
	out := make([]Event, len(c.events))
	for i, e := range c.events {
		out[i] = *e
	}
	return out
}

// sortKinds returns kinds without repeats, in KindOrder. A kind KindOrder does
// not list goes last, so an unknown name is kept rather than lost.
func sortKinds(kinds []EventType) []EventType {
	rank := func(k EventType) int {
		if i := slices.Index(KindOrder, k); i >= 0 {
			return i
		}
		return len(KindOrder)
	}
	out := slices.Clone(kinds)
	slices.SortStableFunc(out, func(a, b EventType) int { return rank(a) - rank(b) })
	return slices.Compact(out)
}
