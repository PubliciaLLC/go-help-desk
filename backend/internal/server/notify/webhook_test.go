package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/safehttp"
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

func (f fakeWebhookStore) RecordWebhookDelivery(ctx context.Context, id uuid.UUID, url string, d authstore.WebhookDelivery) error {
	return nil
}

type fakeWebhookStoreErrOnRecord struct {
	err error
}

func (f fakeWebhookStoreErrOnRecord) ListEnabledWebhooks(ctx context.Context) ([]authstore.WebhookConfig, error) {
	return nil, nil
}

func (f fakeWebhookStoreErrOnRecord) RecordWebhookDelivery(ctx context.Context, id uuid.UUID, url string, d authstore.WebhookDelivery) error {
	return f.err
}

type recorded struct {
	ID  uuid.UUID
	URL string
	D   authstore.WebhookDelivery
}

type recordingWebhookStore struct {
	hooks []authstore.WebhookConfig
	got   chan recorded
	err   error
}

func (r recordingWebhookStore) ListEnabledWebhooks(ctx context.Context) ([]authstore.WebhookConfig, error) {
	return r.hooks, nil
}

func (r recordingWebhookStore) RecordWebhookDelivery(ctx context.Context, id uuid.UUID, url string, d authstore.WebhookDelivery) error {
	r.got <- recorded{ID: id, URL: url, D: d}
	return r.err
}

func TestSend_LogsFailedDelivery(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		serverFails bool // if true, server closes before sending a response
		wantWarn    bool // a failed delivery is logged at WARN; a success is not logged at all
		wantLogKeys []string
	}{
		{
			name:   "200 success logs nothing",
			status: 200,
		},
		{
			name:   "204 success logs nothing",
			status: 204,
		},
		{
			name:        "400 client error logs delivery rejected",
			status:      400,
			wantWarn:    true,
			wantLogKeys: []string{"webhook delivery rejected", "status=400", "webhook_id", "payload_format"},
		},
		{
			name:        "500 server error logs delivery rejected",
			status:      500,
			wantWarn:    true,
			wantLogKeys: []string{"webhook delivery rejected", "status=500", "webhook_id", "payload_format"},
		},
		{
			name:        "transport failure logs delivery failed",
			serverFails: true,
			wantWarn:    true,
			wantLogKeys: []string{"webhook delivery failed", "webhook_id", "payload_format"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			// Debug, so that a success which logged anything at any level
			// (not just WARN) would show up and fail the Empty check below.
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

			if tc.wantWarn {
				require.Contains(t, logged, "level=WARN", "a failed delivery must be logged at WARN")
			} else {
				require.Empty(t, logged, "a successful delivery must not log anything")
			}
			for _, key := range tc.wantLogKeys {
				require.Contains(t, logged, key, "expected %q in log", key)
			}

			// The URL path carries the secret for Slack/Discord/JIRA-style
			// hooks, so no row — success, rejection or transport failure —
			// may ever put it in the log.
			require.NotContains(t, logged, "sekrit-token", "the log must never contain the webhook URL's secret")
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

func (f fakeWebhookStoreErr) RecordWebhookDelivery(ctx context.Context, id uuid.UUID, url string, d authstore.WebhookDelivery) error {
	return nil
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

// ── Delivery Error Classification ─────────────────────────────────────────────

func TestClassifyDeliveryError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "DNS error",
			err: &url.Error{
				Op:  "dial",
				Err: &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "x.invalid"}},
			},
			want: DeliveryDNS,
		},
		{
			name: "blocked address",
			err: &url.Error{
				Op: "dial",
				Err: &net.OpError{
					Op:  "dial",
					Err: fmt.Errorf("%w to private address 10.0.0.1", safehttp.ErrBlockedAddress),
				},
			},
			want: DeliveryBlockedAddress,
		},
		{
			name: "connection refused",
			err: &net.OpError{
				Op:  "dial",
				Err: syscall.ECONNREFUSED,
			},
			want: DeliveryConnection,
		},
		{
			name: "EOF",
			err: &url.Error{
				Op:  "Post",
				Err: io.EOF,
			},
			want: DeliveryConnection,
		},
		{
			name: "TLS certificate verification error",
			err: &url.Error{
				Op:  "Post",
				Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
			},
			want: DeliveryTLS,
		},
		{
			name: "context deadline exceeded",
			err: &url.Error{
				Op:  "Post",
				Err: context.DeadlineExceeded,
			},
			want: DeliveryTimeout,
		},
		{
			name: "other error string",
			err:  errors.New("http: server gave HTTP response to HTTPS client"),
			want: DeliveryOther,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyDeliveryError(tc.err)
			require.Equal(t, tc.want, got)
		})
	}
}

// ── Send Returns Result ───────────────────────────────────────────────────────

func TestSend_ReturnsTheResult(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		serverFails   bool
		serverTimeout bool
		wantStatus    int
		wantError     string
	}{
		{
			name:       "200 success",
			status:     200,
			wantStatus: 200,
			wantError:  "",
		},
		{
			name:       "204 success",
			status:     204,
			wantStatus: 204,
			wantError:  "",
		},
		{
			name:       "400 client error",
			status:     400,
			wantStatus: 400,
			wantError:  DeliveryHTTPStatus,
		},
		{
			name:       "500 server error",
			status:     500,
			wantStatus: 500,
			wantError:  DeliveryHTTPStatus,
		},
		{
			name:        "connection closed",
			serverFails: true,
			wantStatus:  0,
			wantError:   DeliveryConnection,
		},
		{
			name:          "timeout",
			serverTimeout: true,
			wantStatus:    0,
			wantError:     DeliveryTimeout,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.serverFails {
					hj, ok := w.(http.Hijacker)
					require.True(t, ok)
					conn, _, err := hj.Hijack()
					require.NoError(t, err)
					conn.Close()
					return
				}
				if tc.serverTimeout {
					time.Sleep(500 * time.Millisecond)
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			disp := &WebhookDispatcher{
				client: server.Client(),
				log:    slog.Default(),
			}
			if tc.serverTimeout {
				disp.client.Timeout = 100 * time.Millisecond
			}

			hook := authstore.WebhookConfig{
				ID:  uuid.New(),
				URL: server.URL + "/hook",
			}

			before := time.Now().UTC()
			result := disp.send(hook, []byte(`{"test":"data"}`))
			after := time.Now().UTC()

			require.Equal(t, tc.wantStatus, result.Status)
			require.Equal(t, tc.wantError, result.Error)
			require.True(t, result.At.After(before) || result.At.Equal(before))
			require.True(t, result.At.Before(after) || result.At.Equal(after))
		})
	}
}

// ── Dispatch Records Results ──────────────────────────────────────────────────

func TestDispatch_RecordsEachHooksOwnResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	hookA := authstore.WebhookConfig{
		ID:      uuid.New(),
		URL:     server.URL + "/ok",
		Events:  []string{"*"},
		Enabled: true,
	}
	hookB := authstore.WebhookConfig{
		ID:      uuid.New(),
		URL:     server.URL + "/bad",
		Events:  []string{"*"},
		Enabled: true,
	}

	recordedChan := make(chan recorded, 2)
	store := recordingWebhookStore{
		hooks: []authstore.WebhookConfig{hookA, hookB},
		got:   recordedChan,
	}

	disp := &WebhookDispatcher{
		store:   store,
		client:  server.Client(),
		baseURL: fixtureBaseURL,
		log:     slog.Default(),
	}

	require.NoError(t, disp.Dispatch(context.Background(), fixtureReplyEvent(false)))

	// Wait for both records.
	var recs [2]recorded
	for i := 0; i < 2; i++ {
		select {
		case rec := <-recordedChan:
			recs[i] = rec
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for webhook delivery record")
		}
	}

	// Find the records by hook ID.
	recA, recB := recs[0], recs[1]
	if recA.ID != hookA.ID {
		recA, recB = recB, recA
	}

	require.Equal(t, hookA.ID, recA.ID)
	require.Equal(t, hookA.URL, recA.URL)
	require.Equal(t, 200, recA.D.Status)
	require.Equal(t, "", recA.D.Error)

	require.Equal(t, hookB.ID, recB.ID)
	require.Equal(t, hookB.URL, recB.URL)
	require.Equal(t, 500, recB.D.Status)
	require.Equal(t, DeliveryHTTPStatus, recB.D.Error)
}

func TestDispatch_RecordFailureIsLoggedNotFatal(t *testing.T) {
	hookID := uuid.New()
	storeErr := errors.New("record failed")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	recordedChan := make(chan recorded, 1)
	store := recordingWebhookStore{
		hooks: []authstore.WebhookConfig{{
			ID:      hookID,
			URL:     server.URL + "/hook",
			Events:  []string{"*"},
			Enabled: true,
		}},
		got: recordedChan,
		err: storeErr,
	}

	// Use a custom logger to capture log messages without race conditions.
	logChan := make(chan string, 10)
	logger := slog.New(slog.NewTextHandler(
		&testLogWriter{logChan},
		&slog.HandlerOptions{Level: slog.LevelDebug},
	))

	disp := &WebhookDispatcher{
		store:   store,
		client:  server.Client(),
		baseURL: fixtureBaseURL,
		log:     logger,
	}

	require.NoError(t, disp.Dispatch(context.Background(), fixtureReplyEvent(false)))

	// Wait for the record attempt to be made (this includes the failed log).
	select {
	case <-recordedChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for record attempt")
	}

	// Collect the log messages.
	var logged string
	select {
	case msg := <-logChan:
		logged = msg
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for log message")
	}

	require.Contains(t, logged, "webhook delivery result not recorded")
	require.Contains(t, logged, hookID.String())
	require.NotContains(t, logged, server.URL, "the log must not contain the webhook URL")
}

// testLogWriter is a write that sends each write to a channel for test collection.
type testLogWriter struct {
	ch chan string
}

func (w *testLogWriter) Write(p []byte) (n int, err error) {
	select {
	case w.ch <- string(p):
	default:
	}
	return len(p), nil
}

func TestDispatch_RecordedResultCarriesNoSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("sekrit-body"))
	}))
	defer server.Close()

	recordedChan := make(chan recorded, 1)
	hookID := uuid.New()
	store := recordingWebhookStore{
		hooks: []authstore.WebhookConfig{{
			ID:      hookID,
			URL:     server.URL + "/sekrit-token?key=sekrit-q",
			Secret:  "hmac-sekrit",
			Events:  []string{"*"},
			Enabled: true,
		}},
		got: recordedChan,
	}

	disp := &WebhookDispatcher{
		store:   store,
		client:  server.Client(),
		baseURL: fixtureBaseURL,
		log:     slog.Default(),
	}

	require.NoError(t, disp.Dispatch(context.Background(), fixtureReplyEvent(false)))

	// Wait for the record.
	var rec recorded
	select {
	case rec = <-recordedChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for record")
	}

	// Check that no secrets appear in the recorded data.
	recStr := fmt.Sprintf("%+v", rec.D)
	require.NotContains(t, recStr, "sekrit-token")
	require.NotContains(t, recStr, "sekrit-q")
	require.NotContains(t, recStr, "hmac-sekrit")
	require.NotContains(t, recStr, "sekrit-body")
}

func TestDeliveryErrors_MatchTheList(t *testing.T) {
	// Read the migration file and verify each error class is listed.
	wd, err := os.Getwd()
	require.NoError(t, err)

	migrationPath := filepath.Join(wd, "../../database/migrations/000034_webhook_last_delivery.up.sql")
	migrationBytes, err := os.ReadFile(migrationPath)
	require.NoError(t, err, "could not read migration file at %s", migrationPath)

	migration := string(migrationBytes)

	// Check that DeliveryErrors list exactly matches the migration CHECK.
	for _, errClass := range DeliveryErrors {
		if errClass != "" { // Empty string is the success case
			require.Contains(t, migration, fmt.Sprintf("'%s'", errClass),
				"error class %q not found in migration CHECK constraint", errClass)
		}
	}

	// Verify the list is exactly right by checking order.
	require.Equal(t, []string{
		DeliveryHTTPStatus,
		DeliveryTimeout,
		DeliveryDNS,
		DeliveryTLS,
		DeliveryBlockedAddress,
		DeliveryConnection,
		DeliveryOther,
	}, DeliveryErrors)
}
