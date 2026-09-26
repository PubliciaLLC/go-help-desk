package notify

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// ── Event Validation ──────────────────────────────────────────────────────────

func TestIsWebhookEvent(t *testing.T) {
	cases := []struct {
		name string
		e    string
		want bool
	}{
		{name: "ticket.created", e: "ticket.created", want: true},
		{name: "ticket.assigned", e: "ticket.assigned", want: true},
		{name: "ticket.status_changed", e: "ticket.status_changed", want: true},
		{name: "ticket.replied", e: "ticket.replied", want: true},
		{name: "ticket.resolved", e: "ticket.resolved", want: true},
		{name: "ticket.closed", e: "ticket.closed", want: true},
		{name: "ticket.reopened", e: "ticket.reopened", want: true},
		{name: "ticket.linked", e: "ticket.linked", want: true},
		{name: "star", e: "*", want: true},
		{name: "typo: ticket.creatd", e: "ticket.creatd", want: false},
		{name: "empty string", e: "", want: false},
		{name: "Ticket.Created uppercase", e: "Ticket.Created", want: false},
		{name: "space prefix", e: " ticket.created", want: false},
		{name: "guest.link_resent excluded", e: "guest.link_resent", want: false},
		{name: "double star", e: "**", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsWebhookEvent(tc.e)
			require.Equal(t, tc.want, got)
		})
	}
}

// ── Logging ───────────────────────────────────────────────────────────────────

func TestWithoutURL(t *testing.T) {
	t.Run("unwraps url.Error", func(t *testing.T) {
		inner := errors.New("connection refused")
		outer := &url.Error{Op: "Post", URL: "https://example.com/tok", Err: inner}
		got := withoutURL(outer)
		require.Equal(t, inner, got)
	})
	t.Run("returns other errors unchanged", func(t *testing.T) {
		e := errors.New("some error")
		got := withoutURL(e)
		require.Equal(t, e, got)
	})
}

type fakeWebhookStore []authstore.WebhookConfig

func (f fakeWebhookStore) ListEnabledWebhooks(ctx context.Context) ([]authstore.WebhookConfig, error) {
	return []authstore.WebhookConfig(f), nil
}

func TestSend_LogsFailedDelivery(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		serverFails   bool // if true, server closes before sending a response
		wantLogLevel  slog.Level
		wantLogKeys   []string
		shouldNotHave []string
	}{
		{
			name:         "2xx success logs nothing",
			status:       200,
			wantLogLevel: slog.LevelWarn + 1, // higher than Warn, so no warn message
			wantLogKeys:  []string{},
		},
		{
			name:         "204 success logs nothing",
			status:       204,
			wantLogLevel: slog.LevelWarn + 1,
			wantLogKeys:  []string{},
		},
		{
			name:         "400 client error logs delivery rejected",
			status:       400,
			wantLogLevel: slog.LevelWarn,
			wantLogKeys:  []string{"webhook delivery rejected", "status", "webhook_id", "payload_format"},
		},
		{
			name:         "500 server error logs delivery rejected",
			status:       500,
			wantLogLevel: slog.LevelWarn,
			wantLogKeys:  []string{"webhook delivery rejected", "status", "webhook_id", "payload_format"},
		},
		{
			name:          "transport failure logs delivery failed",
			serverFails:   true,
			wantLogLevel:  slog.LevelWarn,
			wantLogKeys:   []string{"webhook delivery failed", "webhook_id", "payload_format"},
			shouldNotHave: []string{"sekrit-token"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.serverFails {
					// Close the connection without responding
					hj, ok := w.(http.Hijacker)
					require.True(t, ok)
					conn, _, err := hj.Hijack()
					require.NoError(t, err)
					conn.Close()
					return
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			hookID := uuid.New()
			disp := &WebhookDispatcher{
				client: server.Client(),
				log:    logger,
			}

			hook := authstore.WebhookConfig{
				ID:            hookID,
				URL:           server.URL + "/api/webhooks/1/sekrit-token",
				PayloadFormat: "raw",
			}

			payload := []byte(`{"test":"data"}`)
			disp.send(hook, payload)

			logged := buf.String()
			for _, key := range tc.wantLogKeys {
				require.Contains(t, logged, key, "expected key %q in log", key)
			}
			for _, key := range tc.shouldNotHave {
				require.NotContains(t, logged, key, "should not contain %q in log", key)
			}
		})
	}
}

func TestDispatch_UnknownFormatIsLoggedAndSkipped(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	badID := uuid.New()
	okID := uuid.New()

	pathChan := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathChan <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := fakeWebhookStore{
		{
			ID:            badID,
			PayloadFormat: "xml", // unknown format
			Events:        []string{"*"},
			URL:           server.URL + "/bad",
		},
		{
			ID:            okID,
			PayloadFormat: "raw",
			Events:        []string{"*"},
			URL:           server.URL + "/ok",
		},
	}

	disp := &WebhookDispatcher{
		store:   store,
		client:  server.Client(),
		baseURL: fixtureBaseURL,
		log:     logger,
	}

	require.NoError(t, disp.Dispatch(context.Background(), fixtureReplyEvent(false)))

	// Check the log immediately - the skip happens before any goroutine starts
	logged := buf.String()
	require.Contains(t, logged, "webhook skipped")
	require.Contains(t, logged, "payload_format=xml")
	require.Contains(t, logged, badID.String())
	require.Contains(t, logged, "event=ticket.replied")
	require.NotContains(t, logged, okID.String()) // healthy hook logs nothing

	// Wait for the good hook to be delivered
	select {
	case path := <-pathChan:
		require.Equal(t, "/ok", path)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for webhook delivery")
	}
}

// TestDispatch_WildcardExcludesGuestLinkResent pins #212: notify.WebhookEvents
// deliberately excludes guest.link_resent, and IsWebhookEvent correctly
// rejects it on webhook create/update, but Dispatch itself had no event-type
// filter — hookSubscribes returns true for a "*" (or legacy empty-events)
// hook regardless of event type, so every wildcard subscription received it
// anyway. A "*" hook and a legacy empty-events hook must each receive zero
// deliveries for EventGuestLinkResent.
func TestDispatch_WildcardExcludesGuestLinkResent(t *testing.T) {
	deliveries := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deliveries <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := fakeWebhookStore{
		{ID: uuid.New(), PayloadFormat: "raw", Events: []string{"*"}, URL: server.URL + "/wildcard"},
		{ID: uuid.New(), PayloadFormat: "raw", Events: nil, URL: server.URL + "/legacy-empty"},
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	disp := &WebhookDispatcher{store: store, client: server.Client(), baseURL: fixtureBaseURL, log: logger}

	ev := notification.Event{
		Type:           notification.EventGuestLinkResent,
		TicketID:       fixtureTicketID,
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000001",
		Subject:        "Printer jammed",
	}
	require.NoError(t, disp.Dispatch(context.Background(), ev))

	select {
	case path := <-deliveries:
		t.Fatalf("guest.link_resent must never reach a webhook, wildcard or not; got a delivery to %s", path)
	case <-time.After(200 * time.Millisecond):
		// No delivery within the window: correct.
	}

	// The same event type IS delivered when it's one WebhookEvents actually
	// lists, proving the filter isn't just silently dropping everything.
	okEv := fixtureReplyEvent(false)
	require.NoError(t, disp.Dispatch(context.Background(), okEv))
	select {
	case <-deliveries:
	case <-time.After(2 * time.Second):
		t.Fatal("a real webhook event type must still be delivered to a wildcard hook")
	}
}

// ── Dispatch boundary logging (#213) ─────────────────────────────────────────

type fakeWebhookStoreErr struct{ err error }

func (f fakeWebhookStoreErr) ListEnabledWebhooks(context.Context) ([]authstore.WebhookConfig, error) {
	return nil, f.err
}

// TestDispatch_ListEnabledWebhooksFailureIsLogged pins #213: a database error
// listing hooks used to `return nil` silently, indistinguishable from
// "nothing subscribed." It must now be logged at the dispatch boundary.
func TestDispatch_ListEnabledWebhooksFailureIsLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	storeErr := errors.New("connection reset by peer")
	disp := &WebhookDispatcher{store: fakeWebhookStoreErr{err: storeErr}, log: logger}

	err := disp.Dispatch(context.Background(), fixtureReplyEvent(false))
	require.NoError(t, err, "a store failure is non-fatal to the caller")

	logged := buf.String()
	require.Contains(t, logged, "could not list enabled webhooks")
	require.Contains(t, logged, "connection reset by peer")
	require.Contains(t, logged, "event=ticket.replied")
}

// TestDispatch_MarshalFailureIsLogged pins #213's other silent path: the
// initial json.Marshal(event) failing used to `return nil` with no log line.
func TestDispatch_MarshalFailureIsLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	store := fakeWebhookStore{
		{ID: uuid.New(), PayloadFormat: "raw", Events: []string{"*"}, URL: "http://unused.invalid"},
	}
	disp := &WebhookDispatcher{store: store, log: logger}

	// A channel value in Payload cannot be marshalled to JSON.
	ev := notification.Event{
		Type:           notification.EventTicketReplied,
		TicketID:       fixtureTicketID,
		Payload:        map[string]any{"bad": make(chan int)},
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000001",
		Subject:        "Printer jammed",
	}
	err := disp.Dispatch(context.Background(), ev)
	require.NoError(t, err, "a marshal failure is non-fatal to the caller")

	logged := buf.String()
	require.Contains(t, logged, "could not be marshalled")
	require.Contains(t, logged, "event=ticket.replied")
}

// TestNewWebhookDispatcher_NilLoggerDefaultsInsteadOfPanicking pins that a nil
// logger can never reach send's unrecovered goroutine: calling a method on a
// nil *slog.Logger panics, and a panic in that goroutine takes the whole
// process down. A caller that skips this constructor and builds the struct
// literal directly (as other tests in this file do, by design, to bypass
// safehttp) is still responsible for setting log itself; this only guards
// the constructor path.
func TestNewWebhookDispatcher_NilLoggerDefaultsInsteadOfPanicking(t *testing.T) {
	disp := NewWebhookDispatcher(nil, fixtureBaseURL, nil)
	require.NotNil(t, disp.log)
}
