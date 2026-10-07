package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/outboxstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
)

// #164: guest resend answered identically for a match and a miss, but a
// match rotated the token and dialled the mail server on the request, and a
// miss ran one SELECT. Its timing said whether a tracking number and an
// address went together. The request now looks nothing up and changes
// nothing: it queues, and the matching happens at send time.

func resend(t *testing.T, h *harness, tracking, email string) {
	t.Helper()
	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/resend", "",
		map[string]any{"tracking_number": tracking, "email": email})
	res.Body.Close()
	require.Equal(t, http.StatusAccepted, res.StatusCode)
}

// What a request does by itself, before any send: a match and a miss queue
// the same event, and the link the customer holds still works.
func TestGuestResend_TheRequestDoesTheSameWorkForAMatchAndAMiss(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))
	tk, token := seedGuestTicket(t, h)
	h.dispatcher.record = true

	resend(t, h, string(tk.TrackingNumber), "guest@test.local")  // a match
	resend(t, h, string(tk.TrackingNumber), "someone@else.test") // a miss
	require.Len(t, h.dispatcher.queued, 2)

	match, miss := h.dispatcher.queued[0], h.dispatcher.queued[1]
	for _, ev := range []notification.Event{match, miss} {
		require.Equal(t, notification.EventGuestLinkResent, ev.Type)
		require.True(t, ev.GuestLink)
		require.Empty(t, ev.GuestToken, "the request created a token")
		require.Zero(t, ev.TicketID, "the request looked the ticket up")
	}
	// Only what the caller typed differs.
	miss.Recipient, miss.OccurredAt = match.Recipient, match.OccurredAt
	require.Equal(t, match, miss)

	still := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	defer still.Body.Close()
	require.Equal(t, http.StatusOK, still.StatusCode, "the request rotated the link itself")
}

// A ticket closed between the request and the send gets no link: closing
// revokes, and a send must not hand out a fresh credential to a ticket that
// no longer accepts one.
func TestGuestLink_ATicketClosedBeforeTheSendGetsNoLink(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	tk, _ := seedGuestTicket(t, h)
	h.dispatcher.record = true
	resend(t, h, string(tk.TrackingNumber), "guest@test.local")
	require.Len(t, h.dispatcher.queued, 1)

	require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
	_, ok, err := h.srv.PrepareGuestLink(ctx, h.dispatcher.queued[0])
	require.NoError(t, err)
	require.False(t, ok, "a closed ticket was sent a fresh link")
}

// End to end through the real outbox and worker: the request writes a row,
// the row never holds the raw token, and the worker's send delivers a link
// that works.
func TestGuestResend_ThroughTheOutbox(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	tk, old := seedGuestTicket(t, h)

	store := outboxstore.New(h.q)
	h.dispatcher.outbox = notify.NewOutboxDispatcher(store, []string{"email"}, nil)
	resend(t, h, string(tk.TrackingNumber), "guest@test.local")

	rows, err := store.Claim(ctx, 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotContains(t, string(rows[0].Event), old)
	require.Equal(t, 1, rows[0].Attempts)
	// Release it for the worker, as if the lease had run out.
	require.NoError(t, store.Retry(ctx, rows[0].ID, time.Now().Add(-time.Second), "released by test"))

	email := &recordingChannel{}
	w := notify.NewWorker(store, map[string]notification.Dispatcher{
		"email": notify.NewGuestLinkDispatcher(email, h.srv.PrepareGuestLink),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err = w.RunOnce(ctx)
	require.NoError(t, err)

	require.Len(t, email.got, 1, "the worker did not send the link")
	sent := email.got[0]
	require.NotEmpty(t, sent.GuestToken)
	require.Equal(t, "guest@test.local", sent.Recipient)

	fresh := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", sent.GuestToken, nil)
	fresh.Body.Close()
	require.Equal(t, http.StatusOK, fresh.StatusCode, "the link that was sent does not work")
	stale := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", old, nil)
	stale.Body.Close()
	require.Equal(t, http.StatusNotFound, stale.StatusCode, "the send did not rotate the old link away")

	// Make the row due again if it still exists; a delivered row was deleted,
	// so there is nothing left to claim.
	require.NoError(t, store.Retry(ctx, rows[0].ID, time.Now().Add(-time.Second), "probe"))
	left, err := store.Claim(ctx, 10, time.Minute)
	require.NoError(t, err)
	require.Empty(t, left, "a delivered row was left in the outbox")
}

type recordingChannel struct{ got []notification.Event }

func (r *recordingChannel) Dispatch(_ context.Context, ev notification.Event) error {
	r.got = append(r.got, ev)
	return nil
}
