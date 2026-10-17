package notify

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

func resetEvent() notification.Event {
	return notification.Event{Type: notification.EventPasswordResetRequested, Recipient: "ada@example.com"}
}

// #451: a reset request carries the address that was typed, so it is queued
// for the verification channel (the signup mail's) and for nothing else. The
// email channel carries any event with a Recipient, which is why this one has
// to be refused there by name.
func TestOutboxDispatcher_PasswordResetGoesOnlyToTheVerificationChannel(t *testing.T) {
	store := newFakeOutbox()
	d := NewOutboxDispatcher(store, []string{"email", "webhook", "verification"}, nil)

	require.NoError(t, d.Dispatch(context.Background(), resetEvent()))

	require.Equal(t, []string{"verification"}, store.channelsQueued())
}

// The address typed is kept in the row so the send can find the account; the
// link's token is not, because it does not exist yet.
func TestOutboxRecord_PasswordResetKeepsTheAddressAndNoToken(t *testing.T) {
	b, err := encodeEvent(resetEvent())
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(b)), "token")

	got, err := decodeEvent(b)
	require.NoError(t, err)
	require.Equal(t, notification.EventPasswordResetRequested, got.Type)
	require.Equal(t, "ada@example.com", got.Recipient)
}

func TestVerificationDispatcher_SendsAPasswordResetThroughItsOwnStep(t *testing.T) {
	var reset []notification.Event
	var signups []uuid.UUID
	d := NewVerificationDispatcher(func(_ context.Context, id uuid.UUID) error { signups = append(signups, id); return nil }).
		WithPasswordReset(func(_ context.Context, ev notification.Event) error { reset = append(reset, ev); return nil })

	require.NoError(t, d.Dispatch(context.Background(), resetEvent()))

	require.Len(t, reset, 1)
	require.Equal(t, "ada@example.com", reset[0].Recipient)
	require.Empty(t, signups, "a reset request went to the signup step")
}

// A dispatcher wired without the reset step must say so rather than settle the
// row as sent: a request that vanished with no trace is the failure this
// channel exists to prevent.
func TestVerificationDispatcher_APasswordResetWithNoStepFails(t *testing.T) {
	d := NewVerificationDispatcher(func(context.Context, uuid.UUID) error { return nil })
	require.Error(t, d.Dispatch(context.Background(), resetEvent()))
}

func TestSendPasswordResetEmail_LinkComesFromConfigNotTheRequest(t *testing.T) {
	for _, base := range []string{"https://help.example.com", "https://support.example.org"} {
		t.Run(base, func(t *testing.T) {
			addr, received := captureSMTP(t)
			d := dispatcherFor(t, addr)

			require.NoError(t, d.SendPasswordResetEmail("user@example.com", "the-secret-token", base))

			raw := strings.ReplaceAll(<-received, "=\r\n", "")
			require.Contains(t, raw, base+"/reset-password?token=3Dthe-secret-token")
			require.Contains(t, raw, "To: <user@example.com>")
			require.Contains(t, raw, "30 minutes", "the mail says how long the link lasts")
			require.NotContains(t, raw, "127.0.0.1")
		})
	}
}

func TestSendPasswordResetEmail_TemplateFailureIsAPermanentError(t *testing.T) {
	err := brokenDispatcher().SendPasswordResetEmail("to@example.com", "tok", "https://help.example.com")
	require.ErrorIs(t, err, ErrPermanent)
}

func TestSendPasswordResetEmail_SendsNothingWhenEmailIsOff(t *testing.T) {
	d := brokenDispatcher()
	d.cfg.SMTPHost = ""
	require.NoError(t, d.SendPasswordResetEmail("to@example.com", "tok", "https://help.example.com"),
		"with email off there is nothing to deliver and nothing to retry")
}
