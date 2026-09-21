package ticket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// GuestTokenTTL is the outer bound on a guest link, not its usual life.
//
// On an active ticket the token is replaced every time something happens that
// the guest is told about — a public reply, a status change, a resolution, a
// reopen — so thirty days is what a link gets when a ticket goes quiet, not
// what a leaked one gets to enjoy.
const GuestTokenTTL = 30 * 24 * time.Hour

// ErrGuestTokenNotFound is the single answer for every reason a token does not
// resolve: never issued, rotated away, expired, or naming a ticket that has
// been closed or deleted.
//
// One error because the caller must answer 404 for all of them. Distinguishing
// "expired" from "never existed" would confirm to anyone holding a guessed
// token that it had once been real.
var ErrGuestTokenNotFound = errors.New("guest token not found")

// IssueGuestToken mints a token for a ticket and returns the raw value, which
// is the only time it exists outside an email.
//
// Any token the ticket already held is deleted first, so issuing is also
// rotating. There is deliberately no grace period: overlapping tokens would
// leave a leaked link working after the rotation meant to kill it, which is
// most of the reason to rotate.
func (s *Service) IssueGuestToken(ctx context.Context, ticketID uuid.UUID) (string, error) {
	raw, hashed, err := auth.GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generating guest token: %w", err)
	}
	if err := s.store.DeleteGuestTokensForTicket(ctx, ticketID); err != nil {
		return "", fmt.Errorf("clearing previous guest tokens: %w", err)
	}
	if err := s.store.CreateGuestToken(ctx, uuid.New(), ticketID, hashed, time.Now().Add(GuestTokenTTL)); err != nil {
		return "", fmt.Errorf("storing guest token: %w", err)
	}
	return raw, nil
}

// RevokeGuestTokens removes a ticket's access without issuing a replacement.
// Closing a ticket does this.
func (s *Service) RevokeGuestTokens(ctx context.Context, ticketID uuid.UUID) error {
	if err := s.store.DeleteGuestTokensForTicket(ctx, ticketID); err != nil {
		return fmt.Errorf("revoking guest tokens: %w", err)
	}
	return nil
}

// TicketForGuestToken resolves a raw token to the one ticket it names.
//
// The raw value is hashed here and the hash alone reaches the store, so a
// token never appears in a query log. First use is stamped on the row; a
// failure to stamp is not a failure to read, because losing a diagnostic is
// not worth refusing a customer their own ticket.
func (s *Service) TicketForGuestToken(ctx context.Context, raw string) (Ticket, error) {
	if raw == "" {
		return Ticket{}, ErrGuestTokenNotFound
	}
	hashed := auth.HashToken(raw)
	t, err := s.store.TicketByGuestToken(ctx, hashed)
	if err != nil {
		return Ticket{}, err
	}
	_ = s.store.TouchGuestToken(ctx, hashed)
	return t, nil
}

// GuestTicketIDFor resolves a tracking number and address to a ticket id for
// the re-request flow, or ErrGuestTokenNotFound when they do not match a ticket
// that still accepts access.
//
// The caller answers 202 either way, so this never becomes a way to test
// whether a tracking number or an address exists.
func (s *Service) GuestTicketIDFor(ctx context.Context, tn TrackingNumber, email string) (uuid.UUID, error) {
	return s.store.TicketIDByTrackingAndGuestEmail(ctx, tn, email)
}
