package notify

import (
	"context"
	"errors"
	"testing"
	"text/template"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// brokenTemplates has no template by any name, so every render fails the way a
// template that references a missing field would.
func brokenDispatcher() *EmailDispatcher {
	return &EmailDispatcher{
		cfg:       &config.Config{SMTPHost: "mail.example.com", SMTPFrom: "help@example.com", BaseURL: "https://help.example.com"},
		templates: template.New("empty"),
	}
}

// A template that cannot render never will. Dispatch used to return nil, so
// the worker deleted the row and the notification vanished with no trace.
func TestDispatch_TemplateFailureIsAPermanentError(t *testing.T) {
	err := brokenDispatcher().Dispatch(context.Background(), notification.Event{
		Type: notification.EventTicketCreated, TicketID: uuid.New(), Recipient: "to@example.com",
	})
	require.Error(t, err, "a broken template must not look like a delivered mail")
	require.ErrorIs(t, err, ErrPermanent)
}

func TestSendVerificationEmail_TemplateFailureIsAPermanentError(t *testing.T) {
	err := brokenDispatcher().SendVerificationEmail("to@example.com", "tok", "https://help.example.com")
	require.ErrorIs(t, err, ErrPermanent)
}

// Not every error is permanent: a refused connection is retried.
func TestDispatch_OrdinaryErrorsAreNotPermanent(t *testing.T) {
	require.False(t, errors.Is(errors.New("smtp: connection refused"), ErrPermanent))
}

// The worker fails a permanent error on the first attempt: no backoff, no
// seven more tries at a template that cannot change. The reason is kept for
// the admin warning.
func TestWorker_PermanentErrorFailsAtOnce(t *testing.T) {
	store := newFakeOutbox()
	email := &recorder{fail: brokenDispatcherErr()}
	require.NoError(t, NewOutboxDispatcher(store, []string{"email"}, nil).Dispatch(context.Background(), sampleEvent()))
	w := newTestWorker(store, map[string]notification.Dispatcher{"email": email})

	_, err := w.RunOnce(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, email.count())
	require.Len(t, store.failed, 1, "a permanent error was not failed on the first attempt")
	require.Empty(t, store.retries, "a permanent error was rescheduled")
	for _, reason := range store.failed {
		require.Contains(t, reason, "rendering ticket_created.tmpl")
	}

	store.makeDue()
	_, err = w.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, email.count(), "a failed row was sent again")
}

// A permanent error wrapped by a dispatcher in front of the email channel (the
// guest-link step) is still permanent.
func TestWorker_PermanentErrorThroughGuestLinkWrapperFailsAtOnce(t *testing.T) {
	store := newFakeOutbox()
	inner := &recorder{fail: brokenDispatcherErr()}
	wrapped := NewGuestLinkDispatcher(inner, nil)
	require.NoError(t, NewOutboxDispatcher(store, []string{"email"}, nil).Dispatch(context.Background(), sampleEvent()))
	w := newTestWorker(store, map[string]notification.Dispatcher{"email": wrapped})

	_, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, store.failed, 1)
	require.Empty(t, store.retries)
}

// Pin: every other error keeps its retry behaviour (TestWorker_RetriesWithBackoffThenFails
// covers the full run; this one only pins that the first failure is a retry).
func TestWorker_OrdinaryErrorStillRetries(t *testing.T) {
	store := newFakeOutbox()
	email := &recorder{fail: errors.New("smtp: connection refused")}
	require.NoError(t, NewOutboxDispatcher(store, []string{"email"}, nil).Dispatch(context.Background(), sampleEvent()))
	w := newTestWorker(store, map[string]notification.Dispatcher{"email": email})

	_, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, store.retries, 1)
	require.Empty(t, store.failed)
}

func brokenDispatcherErr() error {
	return brokenDispatcher().Dispatch(context.Background(), notification.Event{
		Type: notification.EventTicketCreated, TicketID: uuid.New(), Recipient: "to@example.com",
	})
}
