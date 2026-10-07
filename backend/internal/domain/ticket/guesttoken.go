package ticket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
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
// Called at send time (IssueGuestLink), not inside the transaction of the
// change: since #164 the outbox carries the event, and the raw token must not
// be stored there. A change that rolls back queues nothing, so nothing rotates.
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

// hasGuestRecipient says whether an event about t should carry a guest
// link. The link itself is created when the email is sent (IssueGuestLink),
// not inside the transaction of the change: the outbox stores what it will
// send, and the guest token table holds only hashes (#164).
func hasGuestRecipient(t Ticket) bool {
	return t.GuestEmail != nil && *t.GuestEmail != ""
}

// IssueGuestLink is the send-time half of a guest link (#164). Given an event
// marked GuestLink, it re-reads the ticket and, if the ticket still accepts
// guest access, rotates the token and returns the event carrying the raw
// token, the current guest address and the tracking number. ok is false when
// there is no longer anyone to send it to: the ticket was closed or deleted
// after the event, or has no guest address. The caller then sends nothing.
//
// Rotating here rather than at the change moves the moment the previous link
// dies from the commit to the send — normally seconds later. A rotation does
// not wait on the mail server succeeding: a retry rotates again.
func (s *Service) IssueGuestLink(ctx context.Context, ev notification.Event) (notification.Event, bool, error) {
	// One transaction, the ticket row locked, so that concurrent sends for
	// one ticket — two replicas, or a reclaimed row beside a fresh one —
	// rotate one after another. Outside it, a DELETE and an INSERT on
	// autocommit interleaved into two working links (#164 round 1). The lock
	// also orders a send against a concurrent close: whichever commits first,
	// the other sees it.
	var (
		t     Ticket
		token string
		ok    bool
	)
	err := s.atomic.InTx(ctx, func(st Store, _ audit.Store) error {
		var err error
		t, err = st.GetByIDForUpdate(ctx, ev.TicketID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			return err
		}
		if !hasGuestRecipient(t) || t.StatusID == s.sys.closedID {
			return nil
		}
		token, err = rotateGuestToken(ctx, st, t)
		ok = err == nil
		return err
	})
	if err != nil || !ok {
		return ev, false, err
	}
	ev.GuestToken = token
	ev.Recipient = *t.GuestEmail
	ev.TrackingNumber = string(t.TrackingNumber)
	return ev, true, nil
}

// IssueGuestToken rotates outside a transaction.
//
// Nothing in production calls it: production rotates only at send time, in
// IssueGuestLink, which does so under the ticket's row lock. It exists because a test needs a raw token to drive the HTTP surface
// with, and the alternative is duplicating the hashing in the test package —
// which is exactly where a test stops noticing that hashing happens at all.
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

// RequestGuestLink queues a guest's request for a fresh link, without
// looking anything up.
//
// The lookup used to happen here, on the request: a match rotated the token
// and dialled the mail server, a miss ran one SELECT and returned, and the
// difference in time told anyone holding a tracking number and an address
// whether they went together (#164). Now every request, match or miss, queues
// one identical event; whether it names a ticket is decided when the email
// would be sent, off the request. The event carries what the guest typed, not
// what the ticket holds, and no ticket id: matching is the send-time step's job.
//
// Rotating at send time as well as delivering is deliberate: a re-request is a
// new credential, not a second copy of the old one, so a link someone else may
// have seen stops working when the real customer asks for another.
func (s *Service) RequestGuestLink(ctx context.Context, trackingNumber, email string) error {
	return s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventGuestLinkResent,
		OccurredAt:     time.Now(),
		TrackingNumber: trackingNumber,
		Recipient:      email,
		GuestLink:      true,
	})
}

// guestNotifyTarget is guestRecipient, except that a ticket being closed tells
// nobody.
//
// Closing revokes rather than rotating, so there is no link to send. Sending
// anyway fell back to the account URL — the guest received "see where it
// stands" pointing at a page they have no account to open — and Close() sends
// nothing at all, so the two doors into Closed disagreed.
func guestNotifyTarget(t Ticket, closing bool) string {
	if closing {
		return ""
	}
	return guestRecipient(t)
}
