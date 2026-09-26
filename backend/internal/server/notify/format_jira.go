package notify

import (
	"encoding/json"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// jiraBody is a flat, stable, snake_case document for a JIRA Automation rule
// using the "Incoming webhook" trigger, which exposes the POST body to the
// rule as {{webhookData}}.
//
// What this is not, and cannot be: a JIRA REST integration. The help desk
// holds no JIRA credential (DESIGN.md: no new credential type for this
// feature) and, the limitation that actually matters, no ticket-to-issue
// mapping — nothing on a ticket stores a JIRA issue key, so this body cannot
// name one. That is why there is no top-level "issues" array: JIRA's
// Incoming-webhook trigger runs against the issues listed there only when
// that key is present, and an empty list would run the rule zero times, so
// omitting the field entirely (not sending it as []) is what lets the rule
// still fire. A rule that needs to act on a specific issue looks it up
// itself — e.g. JQL on a custom field matching {{webhookData.tracking_number}}
// — or creates one on ticket.created. Two-way sync and reading JIRA state
// back are plugin territory DESIGN.md defers to v2; this format only ever
// pushes one flat, one-way notification.
type jiraBody struct {
	Event          string `json:"event"`
	TicketID       string `json:"ticket_id"`
	TrackingNumber string `json:"tracking_number"`
	Subject        string `json:"subject"`
	URL            string `json:"url"`
	OccurredAt     string `json:"occurred_at"`

	// Internal is a pointer so it is emitted as `false` (not omitted) on a
	// public reply, but left out entirely for every event type other than
	// ticket.replied, matching the "keys: ... internal/body only on
	// ticket.replied" contract.
	Internal *bool  `json:"internal,omitempty"`
	Body     string `json:"body,omitempty"`

	Status         string `json:"status,omitempty"`
	Priority       string `json:"priority,omitempty"`
	LinkedTicketID string `json:"linked_ticket_id,omitempty"`
	LinkType       string `json:"link_type,omitempty"`
}

func renderJIRA(s summary) ([]byte, error) {
	b := jiraBody{
		Event:          string(s.Event),
		TicketID:       s.TicketID.String(),
		TrackingNumber: s.Ref,
		Subject:        s.Subject,
		URL:            s.URL,
		OccurredAt:     s.OccurredAt.UTC().Format(time.RFC3339),
		Status:         s.StatusName,
		Priority:       s.Priority,
		LinkedTicketID: s.LinkedTicketID,
		LinkType:       s.LinkType,
	}
	if s.Event == notification.EventTicketReplied {
		internal := s.Internal
		b.Internal = &internal
		if !s.Internal {
			b.Body = s.Body
		}
	}
	return json.Marshal(b)
}
