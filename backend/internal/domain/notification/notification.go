package notification

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// EventType identifies the kind of thing that happened to a ticket.
type EventType string

const (
	EventTicketCreated       EventType = "ticket.created"
	EventTicketAssigned      EventType = "ticket.assigned"
	EventTicketStatusChanged EventType = "ticket.status_changed"
	EventTicketReplied       EventType = "ticket.replied"
	EventTicketResolved      EventType = "ticket.resolved"
	EventTicketClosed        EventType = "ticket.closed"
	EventTicketReopened      EventType = "ticket.reopened"
	EventTicketLinked        EventType = "ticket.linked"
)

// Event carries the data for a single lifecycle event on a ticket.
type Event struct {
	Type       EventType
	TicketID   uuid.UUID
	ActorID    *uuid.UUID     // nil for system-generated events
	Payload    map[string]any // event-specific data; do not rely on type assertions in domain code
	OccurredAt time.Time

	// TrackingNumber and Recipient are the only two values an email is allowed
	// to carry, and both must be read off the persisted ticket rather than out
	// of Payload.
	//
	// Payload is a free-form map that mixes in whatever the request supplied —
	// the subject line, the reply body, a guest's address. An email built from
	// it is a message this server sends, from its own domain, containing text
	// somebody else chose: content spoofing, CWE-640. Passing these two as
	// typed fields keeps that mixing impossible rather than merely discouraged.
	//
	// Both are excluded from JSON so the webhook payload shape is unchanged and
	// so a webhook subscriber does not gain a customer's address.
	TrackingNumber string `json:"-"`
	Recipient      string `json:"-"`
}

// Dispatcher delivers events to whatever sinks are registered.
// Callers fire and forget — the dispatcher is responsible for retry/durability.
type Dispatcher interface {
	Dispatch(ctx context.Context, event Event) error
}

// Noop satisfies Dispatcher without doing anything. Use in tests and when all
// notification channels are disabled.
type Noop struct{}

func (Noop) Dispatch(_ context.Context, _ Event) error { return nil }
