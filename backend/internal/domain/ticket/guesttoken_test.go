package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

func guestTicket(t *testing.T, h *harness) (ticket.Ticket, string) {
	t.Helper()
	email := "guest@example.test"
	tk, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject: "Printer broken", Description: "it is", CategoryID: uuid.New(),
		GuestEmail: &email, GuestName: "Ada",
	})
	require.NoError(t, err)
	require.Len(t, h.dispatcher.events, 1)
	tokenOf := h.dispatcher.events[0].GuestToken
	require.NotEmpty(t, tokenOf, "creating a guest ticket must mint a link")
	return tk, tokenOf
}

// The token is the guest's whole credential, so the first question is whether
// it reaches the one ticket it names and nothing else.
func TestGuestToken_ReachesItsOwnTicketAndNoOther(t *testing.T) {
	h := newHarness(t)
	tk, token := guestTicket(t, h)

	got, err := h.svc.TicketForGuestToken(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, tk.ID, got.ID)

	_, err = h.svc.TicketForGuestToken(context.Background(), token+"x")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)

	_, err = h.svc.TicketForGuestToken(context.Background(), "")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)
}

// The raw token must never be what is stored. A test that looked a token up by
// its raw value would not notice the day the hashing stopped.
func TestGuestToken_IsStoredHashedNotRaw(t *testing.T) {
	h := newHarness(t)
	_, token := guestTicket(t, h)

	require.NotContains(t, h.store.guestTokens, token,
		"the raw token must not be a key in the store")
	require.Len(t, h.store.guestTokens, 1)
	for hash := range h.store.guestTokens {
		require.NotEqual(t, token, hash)
		require.Len(t, hash, 64, "sha-256, hex encoded")
	}
}

// A ticket with a reporter account has nobody to send a link to, so minting one
// would be a credential issued for no reason.
func TestGuestToken_NotMintedForAnAccountHoldersTicket(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	_, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject: "Laptop", Description: "broken", CategoryID: uuid.New(),
		ReporterUserID: &reporter,
	})
	require.NoError(t, err)
	require.Empty(t, h.dispatcher.events[0].GuestToken)
	require.Empty(t, h.store.guestTokens)
}

// Decision 2: which updates rotate. A staff reply, a status change, a
// resolution and a reopen each replace the link and carry the replacement. An
// internal note does not — rotating would lock the guest out, and the email
// announcing a new link would disclose that staff had written privately about
// their ticket.
func TestGuestToken_RotatesOnlyOnWhatTheGuestIsTold(t *testing.T) {
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	t.Run("a public reply rotates", func(t *testing.T) {
		h := newHarness(t)
		tk, first := guestTicket(t, h)
		_, err := h.svc.AddReply(context.Background(), tk.ID, "looking into it",
			false, true, "guest@example.test", staff, 7, h.newStatus.ID)
		require.NoError(t, err)

		ev := lastEventOfType(t, h, notification.EventTicketReplied)
		require.NotEmpty(t, ev.GuestToken)
		require.NotEqual(t, first, ev.GuestToken, "the link must change")
		require.Equal(t, 1, h.store.guestTokenCount(tk.ID), "rotation replaces, it does not accumulate")

		_, err = h.svc.TicketForGuestToken(context.Background(), first)
		require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound, "no grace period")
		_, err = h.svc.TicketForGuestToken(context.Background(), ev.GuestToken)
		require.NoError(t, err)
	})

	t.Run("an internal note does not rotate", func(t *testing.T) {
		h := newHarness(t)
		tk, first := guestTicket(t, h)
		_, err := h.svc.AddReply(context.Background(), tk.ID, "cost code 4471",
			true, false, "", staff, 7, h.newStatus.ID)
		require.NoError(t, err)

		ev := lastEventOfType(t, h, notification.EventTicketReplied)
		require.Empty(t, ev.GuestToken, "an internal note must mint nothing")
		require.Empty(t, ev.Recipient, "and must reach nobody")

		_, err = h.svc.TicketForGuestToken(context.Background(), first)
		require.NoError(t, err, "the guest's existing link must keep working")
	})

	t.Run("a status change rotates", func(t *testing.T) {
		h := newHarness(t)
		tk, first := guestTicket(t, h)
		_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.resolvedStatus.ID, staff)
		require.NoError(t, err)

		ev := lastEventOfType(t, h, notification.EventTicketStatusChanged)
		require.NotEmpty(t, ev.GuestToken)
		require.NotEqual(t, first, ev.GuestToken)
		require.Equal(t, "guest@example.test", ev.Recipient)
	})

	t.Run("a resolution rotates", func(t *testing.T) {
		h := newHarness(t)
		tk, first := guestTicket(t, h)
		_, err := h.svc.Resolve(context.Background(), tk.ID, "replaced the drum", staff)
		require.NoError(t, err)

		ev := lastEventOfType(t, h, notification.EventTicketResolved)
		require.NotEmpty(t, ev.GuestToken)
		require.NotEqual(t, first, ev.GuestToken)
	})

	t.Run("an assignment does not rotate", func(t *testing.T) {
		h := newHarness(t)
		tk, first := guestTicket(t, h)
		assignee := uuid.New()
		_, err := h.svc.Assign(context.Background(), tk.ID, &assignee, nil, staff)
		require.NoError(t, err)

		_, err = h.svc.TicketForGuestToken(context.Background(), first)
		require.NoError(t, err, "internal bookkeeping must not lock the guest out")
	})
}

// Closing revokes without replacing: a closed ticket accepts nothing, so a live
// link to it is a credential outliving the thing it reaches.
func TestGuestToken_ClosingRevokesAndReopeningIssuesAfresh(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, first := guestTicket(t, h)

	require.NoError(t, h.svc.Close(context.Background(), tk.ID, staff))
	require.Equal(t, 0, h.store.guestTokenCount(tk.ID), "closing leaves no token")
	_, err := h.svc.TicketForGuestToken(context.Background(), first)
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)

	_, err = h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, staff)
	require.NoError(t, err)

	ev := lastEventOfType(t, h, notification.EventTicketReopened)
	require.NotEmpty(t, ev.GuestToken, "reopening must issue a way back in")
	require.NotEqual(t, first, ev.GuestToken)
	_, err = h.svc.TicketForGuestToken(context.Background(), ev.GuestToken)
	require.NoError(t, err)
}

// The re-request flow needs both halves to match, and must not resurrect access
// that closing revoked.
func TestGuestToken_ReRequestNeedsBothHalvesAndRespectsClosure(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, _ := guestTicket(t, h)
	ctx := context.Background()

	id, err := h.svc.GuestTicketIDFor(ctx, tk.TrackingNumber, "GUEST@EXAMPLE.TEST")
	require.NoError(t, err, "the address match is case-insensitive")
	require.Equal(t, tk.ID, id)

	_, err = h.svc.GuestTicketIDFor(ctx, tk.TrackingNumber, "someone@else.test")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound, "the address alone must not be guessable past")

	_, err = h.svc.GuestTicketIDFor(ctx, "GHD-2026-999999", "guest@example.test")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)

	require.NoError(t, h.svc.Close(ctx, tk.ID, staff))
	_, err = h.svc.GuestTicketIDFor(ctx, tk.TrackingNumber, "guest@example.test")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound, "a re-request must not undo revocation")
}

func lastEventOfType(t *testing.T, h *harness, ty notification.EventType) notification.Event {
	t.Helper()
	for i := len(h.dispatcher.events) - 1; i >= 0; i-- {
		if h.dispatcher.events[i].Type == ty {
			return h.dispatcher.events[i]
		}
	}
	t.Fatalf("no %s event was dispatched; got %v", ty, h.dispatcher.types())
	return notification.Event{}
}
