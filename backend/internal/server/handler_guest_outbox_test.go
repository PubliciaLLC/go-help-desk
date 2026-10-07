package server_test

import (
	"context"
	"errors"
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

	// Not just the shape of what is queued: what the request reads. A lookup
	// whose result is thrown away puts the whole timing difference back while
	// queueing exactly the same event (#164 round 1). Postgres counts this
	// transaction's reads per table, and the harness is one transaction.
	// Seeding the ticket already read both tables in this transaction, so a
	// zero here means statistics are off and the check below proves nothing.
	require.NotZero(t, ticketTableReads(t, h), "table statistics are not being counted")
	for _, email := range []string{"guest@test.local", "someone@else.test"} { // a match, a miss
		before := ticketTableReads(t, h)
		resend(t, h, string(tk.TrackingNumber), email)
		require.Equal(t, before, ticketTableReads(t, h),
			"the request for %s read the tickets or guest tokens", email)
	}
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

// failOnce is an email channel whose first send fails, as SMTP does when the
// relay restarts.
type failOnce struct {
	recordingChannel
	failed bool
}

func (f *failOnce) Dispatch(ctx context.Context, ev notification.Event) error {
	if !f.failed {
		f.failed = true
		return errors.New("smtp: connection refused")
	}
	return f.recordingChannel.Dispatch(ctx, ev)
}

// #164 round 1 (MEDIUM): a resend whose first send failed was never retried.
// The per-ticket budget was charged on every attempt, so the retry was
// refused, the step reported "nobody to send to", and the worker deleted the
// row as settled — the guest's old link already dead from attempt one, and no
// email ever sent. The budget is spent once per request, not per attempt.
func TestGuestResend_AFailedSendIsRetriedNotSwallowedByTheBudget(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	tk, _ := seedGuestTicket(t, h)

	store := outboxstore.New(h.q)
	h.dispatcher.outbox = notify.NewOutboxDispatcher(store, []string{"email"}, nil)
	resend(t, h, string(tk.TrackingNumber), "guest@test.local")

	email := &failOnce{}
	w := notify.NewWorker(store, map[string]notification.Dispatcher{
		"email": notify.NewGuestLinkDispatcher(email, h.srv.PrepareGuestLink),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Due at once. Negative, not zero: the harness runs in one transaction,
	// where Postgres now() stays at the transaction's start, so a retry due
	// at Go's time.Now() would never look due to the claim.
	w.Backoff = func(int) time.Duration { return -time.Hour }

	_, err := w.RunOnce(ctx) // attempt 1: the relay is down
	require.NoError(t, err)
	require.Empty(t, email.got)

	_, err = w.RunOnce(ctx) // the retry
	require.NoError(t, err)
	require.Len(t, email.got, 1, "the retry was refused and the link never sent")
	ok := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", email.got[0].GuestToken, nil)
	ok.Body.Close()
	require.Equal(t, http.StatusOK, ok.StatusCode)
}

// ticketTableReads is how many times this transaction has scanned the two
// tables a guest-link lookup reads.
func ticketTableReads(t *testing.T, h *harness) int64 {
	t.Helper()
	var n int64
	require.NoError(t, h.tx.QueryRow(`
		SELECT coalesce(sum(coalesce(seq_scan, 0) + coalesce(idx_scan, 0)), 0)
		FROM pg_stat_xact_user_tables
		WHERE relname IN ('tickets', 'guest_access_tokens')`).Scan(&n))
	return n
}

// The send-time step is for guest links; an account holder's reply mail must
// pass straight through it. Marking every reply GuestLink would drop that
// mail in production (the step answers "nobody to send to"), and the guest
// tests alone did not show it (#164 round 1).
func TestReplyMail_AnAccountHolderStillGetsIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	email := &recordingChannel{}
	h.dispatcher.next = email

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Laptop will not boot", CategoryID: h.catID,
		Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies",
		map[string]any{"body": "Try holding the power button for ten seconds"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var replied []notification.Event
	for _, ev := range email.got {
		if ev.Type == notification.EventTicketReplied {
			replied = append(replied, ev)
		}
	}
	require.Len(t, replied, 1, "the account holder's reply mail did not reach the channel")
	require.False(t, replied[0].GuestLink)
	require.Empty(t, replied[0].GuestToken)
	require.NotEmpty(t, replied[0].Recipient, "the reply mail has nobody to go to")
}
