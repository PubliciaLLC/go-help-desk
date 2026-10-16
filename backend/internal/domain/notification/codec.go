package notification

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrRawGuestToken refuses to queue an event carrying a guest's raw access
// token. The guest token table holds only hashes; a copy in the outbox would
// undo that. Guest links are created by the worker at send time instead.
var ErrRawGuestToken = errors.New("refusing to queue a raw guest token")

// outboxRecord is how an event is stored. Spelled out field by field rather
// than json.Marshal(event), because Event's own JSON is the webhook body and
// deliberately omits the email-only fields this needs.
//
// GuestToken is absent on purpose. TestOutboxRecord_CoversEveryEventField
// fails when a field is added to Event without a decision here.
type outboxRecord struct {
	Type           EventType      `json:"type"`
	TicketID       uuid.UUID      `json:"ticket_id"`
	ActorID        *uuid.UUID     `json:"actor_id,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	OccurredAt     time.Time      `json:"occurred_at"`
	TrackingNumber string         `json:"tracking_number,omitempty"`
	Recipient      string         `json:"recipient,omitempty"`
	Subject        string         `json:"subject,omitempty"`
	StatusName     string         `json:"status_name,omitempty"`
	GuestLink      bool           `json:"guest_link,omitempty"`

	// One change, one event (#506). Rows queued before it have none of the
	// three; DecodeEvent reads them as a one-kind change.
	Kinds   []EventType  `json:"kinds,omitempty"`
	Changes []Change     `json:"changes,omitempty"`
	Ticket  *TicketState `json:"ticket,omitempty"`

	// TargetWebhook is set on the per-hook rows the webhook fan-out queues (#448).
	TargetWebhook uuid.UUID `json:"webhook_id,omitzero"`
	TargetURLHash string    `json:"webhook_url_sha256,omitempty"`

	// Staff mail (#441). StaffUserID is the one person a staff.* row is for;
	// their address is read when it is sent, never stored here.
	StaffUserID   *uuid.UUID  `json:"staff_user_id,omitempty"`
	FromRequester bool        `json:"from_requester,omitempty"`
	StaffReason   StaffSwitch `json:"staff_reason,omitempty"`

	// Read only, never written: how a row queued before #506 said what is now
	// in Ticket. The classification a webhook filter tests, and who held the
	// ticket for staff mail.
	LegacyAttrs           *TicketState `json:"attrs,omitempty"`
	LegacyAssigneeUserID  *uuid.UUID   `json:"assignee_user_id,omitempty"`
	LegacyAssigneeGroupID *uuid.UUID   `json:"assignee_group_id,omitempty"`
}

// EncodeEvent is the outbox row's body for ev. It lives here, not in the
// server's notify package, because the transaction runner writes outbox rows
// and must not import the server.
func EncodeEvent(ev Event) ([]byte, error) {
	if ev.GuestToken != "" {
		return nil, ErrRawGuestToken
	}
	return json.Marshal(outboxRecord{
		Type: ev.Type, TicketID: ev.TicketID, ActorID: ev.ActorID, Payload: ev.Payload,
		OccurredAt: ev.OccurredAt, TrackingNumber: ev.TrackingNumber, Recipient: ev.Recipient,
		Subject: ev.Subject, StatusName: ev.StatusName, GuestLink: ev.GuestLink,
		Kinds: ev.Kinds, Changes: ev.Changes, Ticket: ev.Ticket,
		TargetWebhook: ev.TargetWebhook,
		TargetURLHash: ev.TargetURLHash,
		StaffUserID:   ev.StaffUserID, FromRequester: ev.FromRequester,
		StaffReason: ev.StaffReason,
	})
}

// DecodeEvent reads an outbox row's body.
func DecodeEvent(b []byte) (Event, error) {
	var r outboxRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return Event{}, err
	}
	ev := Event{
		Type: r.Type, TicketID: r.TicketID, ActorID: r.ActorID, Payload: r.Payload,
		OccurredAt: r.OccurredAt, TrackingNumber: r.TrackingNumber, Recipient: r.Recipient,
		Subject: r.Subject, StatusName: r.StatusName, GuestLink: r.GuestLink,
		Kinds: r.Kinds, Changes: r.Changes, Ticket: r.Ticket,
		TargetWebhook: r.TargetWebhook,
		TargetURLHash: r.TargetURLHash,
		StaffUserID:   r.StaffUserID, FromRequester: r.FromRequester,
		StaffReason: r.StaffReason,
	}
	if ev.Ticket == nil && (r.LegacyAttrs != nil || r.LegacyAssigneeUserID != nil || r.LegacyAssigneeGroupID != nil) {
		var s TicketState
		if r.LegacyAttrs != nil {
			s = *r.LegacyAttrs
		}
		if r.LegacyAssigneeUserID != nil {
			s.AssigneeUserID = r.LegacyAssigneeUserID
		}
		if r.LegacyAssigneeGroupID != nil {
			s.AssigneeGroupID = r.LegacyAssigneeGroupID
		}
		ev.Ticket = &s
	}
	return ev, nil
}
