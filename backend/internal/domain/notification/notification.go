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
