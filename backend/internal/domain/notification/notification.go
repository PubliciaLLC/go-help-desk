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
	// EventTicketReplied is a customer-visible reply only. A staff-only note
	// is EventTicketNoteAdded, so a subscriber can ask for one without the
	// other (#448).
	EventTicketReplied   EventType = "ticket.replied"
	EventTicketNoteAdded EventType = "ticket.note_added"
	EventTicketResolved  EventType = "ticket.resolved"
	EventTicketClosed    EventType = "ticket.closed"
	EventTicketReopened  EventType = "ticket.reopened"
	EventTicketLinked    EventType = "ticket.linked"

	// EventTicketPriorityChanged and EventTicketClassified (category, type or
	// item) are raised by changes that announced nothing before (#506). Both
	// are names a webhook can subscribe to; neither carries an id on the wire.
	EventTicketPriorityChanged EventType = "ticket.priority_changed"
	EventTicketClassified      EventType = "ticket.classified"

	// EventGuestLinkResent is a guest asking for a fresh access link, which is
	// not a change to the ticket. It exists so the mail goes out through the
	// one path that sends mail, rather than giving the HTTP layer a mailer of
	// its own.
	EventGuestLinkResent EventType = "guest.link_resent"

	// EventRegistrationVerify queues a signup's verification email (#348).
	// Its payload is only {"pending_id": "<uuid>"}; the address and token are
	// read from the pending registration when the email is sent. Never a
	// webhook event, and no ticket.
	EventRegistrationVerify EventType = "registration.verify"

	// EventStaffAssigned, EventStaffRequesterReplied and EventStaffReopened are
	// the email a staff member gets about a ticket (#441, #464). They are made
	// from EventTicketAssigned, a requester's EventTicketReplied and
	// EventTicketReopened, one per person to be mailed, by
	// notify.StaffFanout, and never dispatched by domain code. Not webhook
	// events: a webhook subscriber already has the events they derive from.
	EventStaffAssigned         EventType = "staff.assigned"
	EventStaffRequesterReplied EventType = "staff.requester_replied"
	EventStaffReopened         EventType = "staff.reopened"
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

	// GuestToken is the raw access token minted for this change, when the
	// recipient is a guest. It is the guest's whole credential, so it is
	// excluded from JSON like the two above — a webhook subscriber receiving
	// one would hold the customer's access to their own ticket.
	GuestToken string `json:"-"`

	// GuestLink marks an event whose email should carry a fresh guest link.
	// The link is created when the email is sent, by ticket.Service's
	// IssueGuestLink, so the raw token is never stored in the outbox (#164).
	// Excluded from JSON like GuestToken: a webhook has no use for it.
	GuestLink bool `json:"-"`

	// Kinds is everything that happened in the change this event announces,
	// as the existing event names, in KindOrder (#506). Consumers match on it:
	// a hook fires when any kind is one it subscribes to. Empty on an event
	// raised the old way and on the rows made by splitting a change (a staff.*
	// row, the requester's mail row): AllKinds then reads it as [Type].
	// Excluded from JSON: the webhook body does not change.
	Kinds []EventType `json:"-"`

	// Changes is each field that moved, from and to, ids and enums only
	// (never text). Excluded from JSON like Kinds.
	Changes []Change `json:"-"`

	// Ticket is the ticket as committed, for the webhook filter and for staff
	// mail (who holds it now). It replaces the Attrs, AssigneeUserID and
	// AssigneeGroupID fields, which were each a part of it. Nil means "not
	// known": an event that names no ticket. Excluded from JSON like Kinds.
	Ticket *TicketState `json:"-"`

	// RoutedAssignment marks an assignment that routing made inside a create,
	// not the person who filed the ticket. Staff mail skips "nobody is mailed
	// about their own action" for it: routing picked the assignee, they did not,
	// and a creator the routing assigned to must still hear of it (as in 1.2.x).
	// Excluded from JSON.
	RoutedAssignment bool `json:"-"`

	// FromRequester marks a ticket.replied that the requester wrote (an
	// account holder with the user role, or a guest) and that is not an
	// internal note. It is what makes a reply a reason to mail staff.
	FromRequester bool `json:"-"`

	// StaffUserID is the one staff member a staff.* event is for. Their
	// address is read at send time rather than copied here, so the outbox
	// holds no address for them and a deleted or disabled account is skipped
	// instead of mailed.
	StaffUserID *uuid.UUID `json:"-"`

	// StaffReason is why StaffUserID is being mailed: SwitchAssigned for the
	// person who holds the ticket, SwitchGroup for a member of the group that
	// does. It words the email, and it is what the send-time check confirms
	// is still true.
	StaffReason StaffSwitch `json:"-"`

	// Subject and StatusName exist for the webhook chat/ITSM renderers
	// (internal/server/notify), which need a human-readable line for events
	// whose Payload carries only ids — ticket.status_changed's Payload is
	// {"new_status_id": <uuid>}, and "moved to 3f2a..." is useless in a Slack
	// message. Adding a key to Payload for this would change the raw webhook
	// body, which is exactly what a "raw" subscription promises not to do,
	// so these follow the same json:"-" precedent as TrackingNumber above
	// instead: readable by a dispatcher, invisible on the wire.
	Subject    string `json:"-"`
	StatusName string `json:"-"`

	// TargetWebhook, when set, makes the event a delivery to that one hook
	// instead of an announcement to all of them: the webhook channel's queued
	// row is first fanned out into one row per subscribed hook, so each hook
	// is retried on its own (#448). Never part of the wire body.
	TargetWebhook uuid.UUID `json:"-"`

	// TargetURLHash is the SHA-256 (hex) of the hook's URL when the per-hook row
	// was queued. A URL can carry a token, so the outbox holds a fingerprint of
	// it and never the URL; at send time a row whose hash no longer matches the
	// hook's URL is dropped (#448). Never part of the wire body.
	TargetURLHash string `json:"-"`
}

// TicketState is the ticket as committed by the change an event announces.
// Priority is ticket.Priority as a string: ticket imports notification, not
// the reverse. The three json keys that predate #506 (category_id, priority,
// assignee_group_id) keep their names, so a row queued before it still reads.
type TicketState struct {
	StatusID        uuid.UUID  `json:"status_id"`
	AssigneeUserID  *uuid.UUID `json:"assignee_user_id,omitempty"`
	AssigneeGroupID *uuid.UUID `json:"assignee_group_id,omitempty"` // nil = no group
	Priority        string     `json:"priority"`
	CategoryID      uuid.UUID  `json:"category_id"`
	TypeID          *uuid.UUID `json:"type_id,omitempty"`
	ItemID          *uuid.UUID `json:"item_id,omitempty"`
	ReopenCount     int        `json:"reopen_count,omitempty"`
}

// AllKinds is what the event announces: its Kinds, or just its Type when it
// has none.
func (e Event) AllKinds() []EventType {
	if len(e.Kinds) > 0 {
		return e.Kinds
	}
	return []EventType{e.Type}
}

// RetryAfterError asks the outbox worker to wait at least After before the
// next attempt, because the receiver said so (a Retry-After header). The
// worker never waits longer than its longest backoff.
type RetryAfterError struct {
	Err   error
	After time.Duration
}

func (e *RetryAfterError) Error() string { return e.Err.Error() }
func (e *RetryAfterError) Unwrap() error { return e.Err }

// Dispatcher delivers events to whatever sinks are registered.
// Callers fire and forget — the dispatcher is responsible for retry/durability.
type Dispatcher interface {
	Dispatch(ctx context.Context, event Event) error
}

// Noop satisfies Dispatcher without doing anything. Use in tests and when all
// notification channels are disabled.
type Noop struct{}

func (Noop) Dispatch(_ context.Context, _ Event) error { return nil }
