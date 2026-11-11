package server_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
)

// A send-time budget is charged once per queued row, and a retry skips the
// charge only if an EARLIER ATTEMPT OF THAT ROW actually charged it. "This is
// attempt 2" is not that: the first attempt may have failed before it reached
// the budget (a lookup that errored) or the count may have failed to write, and
// a retry that skipped on the attempt number alone then went through uncounted,
// the guest's link rotated or the mail sent past a spent limit.

func resendEvent(trackingNumber, email string) notification.Event {
	return notification.Event{
		Type: notification.EventGuestLinkResent, TrackingNumber: trackingNumber, Recipient: email,
	}
}

// A retry of a row that was never charged must be counted, and refused when the
// ticket's budget is spent: nothing is sent and the guest's token is not
// rotated.
func TestGuestResend_ARetryThatWasNeverChargedIsCounted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, _ := seedGuestTicket(t, h)
	ev := resendEvent(string(tk.TrackingNumber), "guest@test.local")

	// Another request spends the ticket's one resend in five minutes.
	_, ok, err := h.srv.PrepareGuestLink(context.Background(), ev)
	require.NoError(t, err)
	require.True(t, ok)

	// A row whose first attempt failed before the budget (no mark on it): attempt 2.
	retry := notify.WithDeliveryForTest(context.Background(), uuid.New(), 2)
	out, ok, err := h.srv.PrepareGuestLink(retry, ev)
	require.NoError(t, err)
	require.False(t, ok, "a retry that was never charged is counted, and the budget is spent")
	require.Empty(t, out.GuestToken, "and no link was made")
}

// A row charged on its first attempt is not charged again on the retry, and
// is still allowed to finish: the budget was spent on it already, and other
// requests counting since must not turn a half-delivered row into a refusal.
func TestGuestResend_ARetryOfAChargedRowIsNotChargedTwice(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, _ := seedGuestTicket(t, h)
	ev := resendEvent(string(tk.TrackingNumber), "guest@test.local")
	row := uuid.New()

	_, ok, err := h.srv.PrepareGuestLink(notify.WithDeliveryForTest(context.Background(), row, 1), ev)
	require.NoError(t, err)
	require.True(t, ok)

	_, ok, err = h.srv.PrepareGuestLink(notify.WithDeliveryForTest(context.Background(), row, 2), ev)
	require.NoError(t, err)
	require.True(t, ok, "the retry of the row that was charged goes ahead")

	requireOneRow(t, h, "guest-resend:"+tk.ID.String(), 1)
}

// The same for the per-account forgot-password mail.
func TestSendPasswordReset_ARetryThatWasNeverChargedIsCounted(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	ev := notification.Event{Type: notification.EventPasswordResetRequested, Recipient: "user@test.local"}

	require.NoError(t, h.srv.SendPasswordReset(context.Background(), ev))
	require.Len(t, h.resetMail.sent, 1)

	retry := notify.WithDeliveryForTest(context.Background(), uuid.New(), 2)
	require.NoError(t, h.srv.SendPasswordReset(retry, ev))
	require.Len(t, h.resetMail.sent, 1, "an uncharged retry is counted, and the account's five minutes are spent")
}

func TestSendPasswordReset_ARetryOfAChargedRowIsNotChargedTwice(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	ev := notification.Event{Type: notification.EventPasswordResetRequested, Recipient: "user@test.local"}
	row := uuid.New()
	h.resetMail.failNext = 1 // the relay is down for the first attempt, which has charged the budget

	require.Error(t, h.srv.SendPasswordReset(notify.WithDeliveryForTest(context.Background(), row, 1), ev))
	require.Empty(t, h.resetMail.sent)

	require.NoError(t, h.srv.SendPasswordReset(notify.WithDeliveryForTest(context.Background(), row, 2), ev))
	require.Len(t, h.resetMail.sent, 1, "the retry of the charged row is sent")
	// The account's counter is at 1 (two attempts, one charge); the other row is
	// the marker that says this queued row was charged.
	counters, markers := 0, 0
	for k, n := range rateRows(t, h, "password-reset:") {
		if strings.HasPrefix(k, "password-reset:once:") {
			markers++
			continue
		}
		counters++
		require.Equal(t, 1, n, "charged once, not once per attempt")
	}
	require.Equal(t, 1, counters)
	require.Equal(t, 1, markers)
}
