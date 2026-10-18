package ticketstore

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Followers (#441). See ticket.Store for why these live beside the ticket
// methods rather than in a store of their own.

func (s *Store) AddFollower(ctx context.Context, ticketID, userID uuid.UUID) (bool, error) {
	n, err := s.q.AddTicketFollower(ctx, dbgen.AddTicketFollowerParams{TicketID: ticketID, UserID: userID})
	if err != nil {
		return false, fmt.Errorf("adding follower: %w", err)
	}
	return n > 0, nil
}

func (s *Store) RemoveFollower(ctx context.Context, ticketID, userID uuid.UUID) (bool, error) {
	n, err := s.q.RemoveTicketFollower(ctx, dbgen.RemoveTicketFollowerParams{TicketID: ticketID, UserID: userID})
	if err != nil {
		return false, fmt.Errorf("removing follower: %w", err)
	}
	return n > 0, nil
}

func (s *Store) ListFollowers(ctx context.Context, ticketID uuid.UUID) ([]ticket.Follower, error) {
	rows, err := s.q.ListTicketFollowers(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing followers: %w", err)
	}
	out := make([]ticket.Follower, len(rows))
	for i, r := range rows {
		out[i] = ticket.Follower{UserID: r.UserID, DisplayName: r.DisplayName, CreatedAt: r.CreatedAt}
	}
	return out, nil
}

func (s *Store) ListMailableFollowerIDs(ctx context.Context, ticketID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.q.ListMailableTicketFollowers(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing mailable followers: %w", err)
	}
	return ids, nil
}
