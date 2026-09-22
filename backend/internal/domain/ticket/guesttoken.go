package ticket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
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

// rotateGuestToken issues a replacement link for a ticket and returns the raw
// token, which is the only moment it exists outside an email.
//
// Takes a Store rather than hanging off Service so a caller can pass the
// transactional store from InTx: a rotation has to commit with the change that
// caused it, or a status change that rolls back leaves the customer holding a
// dead link.
//
// Returns "" for a ticket with no guest address. Account holders sign in; there
// is nobody to send a link to, and minting one would be a credential issued for
// no reason.
//
// Any token the ticket already held is deleted first, so issuing is rotating.
// There is deliberately no grace period: overlapping tokens would leave a
// leaked link working past the rotation meant to kill it, which is most of the
// reason to rotate.
func rotateGuestToken(ctx context.Context, st Store, t Ticket) (string, error) {
	if t.GuestEmail == nil || *t.GuestEmail == "" {
		return "", nil
	}
	raw, hashed, err := auth.GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generating guest token: %w", err)
	}
	if err := st.DeleteGuestTokensForTicket(ctx, t.ID); err != nil {
		return "", fmt.Errorf("clearing previous guest tokens: %w", err)
	}
	if err := st.CreateGuestToken(ctx, uuid.New(), t.ID, hashed, time.Now().Add(GuestTokenTTL)); err != nil {
		return "", fmt.Errorf("storing guest token: %w", err)
	}
	return raw, nil
}

// IssueGuestToken rotates outside a transaction, for the re-request flow.
func (s *Service) IssueGuestToken(ctx context.Context, ticketID uuid.UUID) (string, error) {
	t, err := s.store.GetByID(ctx, ticketID)
	if err != nil {
		return "", err
	}
	return rotateGuestToken(ctx, s.store, t)
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

// guestRecipient is the address a lifecycle notification goes to, or "" when
// the ticket belongs to an account holder.
//
// Only guests are notified of a status change, a resolution or a reopen. An
// account holder signs in and sees it; mailing them every transition would be
// a new stream of email nobody asked for, and the link it carried would be one
// they do not need.
func guestRecipient(t Ticket) string {
	if t.GuestEmail == nil {
		return ""
	}
	return *t.GuestEmail
}

// ResendGuestLink mints a fresh link for a ticket and mails it.
//
// Rotating here as well as delivering is deliberate: a re-request is a new
// credential, not a second copy of the old one, so a link someone else may
// have seen stops working the moment the real customer asks for another.
//
// The caller reports nothing about the outcome — a response that varied would
// turn the re-request endpoint into a way to test whether a ticket or an
// address exists — so the error return is for the caller's logs, not its
// answer.
func (s *Service) ResendGuestLink(ctx context.Context, ticketID uuid.UUID) error {
	t, err := s.store.GetByID(ctx, ticketID)
	if err != nil {
		return err
	}
	token, err := rotateGuestToken(ctx, s.store, t)
	if err != nil {
		return err
	}
	if token == "" {
		return ErrGuestTokenNotFound
	}
	return s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventGuestLinkResent,
		TicketID:       t.ID,
		OccurredAt:     time.Now(),
		TrackingNumber: string(t.TrackingNumber),
		Recipient:      guestRecipient(t),
		GuestToken:     token,
	})
}
