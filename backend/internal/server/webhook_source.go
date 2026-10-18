package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
)

// webhookEventSource reads the ticket as it is now for a redelivery (#448),
// so the delivery history keeps no subject, tracking number or text.
type webhookEventSource struct{ tickets *ticket.Service }

// NewWebhookEventSource is the notify.EventSource backed by the ticket service.
func NewWebhookEventSource(tickets *ticket.Service) notify.EventSource {
	return webhookEventSource{tickets: tickets}
}

func (s webhookEventSource) TicketFacts(ctx context.Context, id uuid.UUID) (notify.TicketFacts, bool, error) {
	t, err := s.tickets.GetByID(ctx, id)
	if errors.Is(err, ticket.ErrNotFound) {
		return notify.TicketFacts{}, false, nil
	}
	if err != nil {
		return notify.TicketFacts{}, false, fmt.Errorf("reading ticket %s: %w", id, err)
	}
	statuses, err := s.tickets.ListStatuses(ctx)
	if err != nil {
		return notify.TicketFacts{}, false, fmt.Errorf("listing statuses: %w", err)
	}
	facts := notify.TicketFacts{Subject: t.Subject, TrackingNumber: string(t.TrackingNumber), Priority: string(t.Priority),
		State: &notification.TicketState{CategoryID: t.CategoryID, Priority: string(t.Priority), AssigneeGroupID: t.AssigneeGroupID}}
	for _, st := range statuses {
		if st.ID == t.StatusID {
			facts.StatusName = st.Name
		}
	}
	return facts, true, nil
}

// PublicReplyBody is the text of a reply on that ticket, if it still exists
// and is not an internal note. A reply that is internal now is refused even
// though the event announced it as public: visibility is read at send time.
func (s webhookEventSource) PublicReplyBody(ctx context.Context, ticketID, replyID uuid.UUID) (string, bool, error) {
	replies, err := s.tickets.ListReplies(ctx, ticketID)
	if err != nil {
		return "", false, fmt.Errorf("listing replies: %w", err)
	}
	for _, r := range replies {
		if r.ID == replyID {
			if r.Internal {
				return "", false, nil
			}
			return r.Body, true, nil
		}
	}
	return "", false, nil
}
