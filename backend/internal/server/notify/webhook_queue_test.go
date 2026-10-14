package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/safehttp"
)

// queueStore is the hook store, the attempt history and the replay store in
// one in-memory fake.
type queueStore struct {
	mu       sync.Mutex
	hooks    map[uuid.UUID]authstore.WebhookConfig
	attempts []authstore.WebhookAttempt
	events   map[uuid.UUID][]byte
}

func newQueueStore(hooks ...authstore.WebhookConfig) *queueStore {
	s := &queueStore{hooks: map[uuid.UUID]authstore.WebhookConfig{}, events: map[uuid.UUID][]byte{}}
	for _, h := range hooks {
		s.hooks[h.ID] = h
	}
	return s
}

func (s *queueStore) ListEnabledWebhooks(context.Context) ([]authstore.WebhookConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []authstore.WebhookConfig
	for _, h := range s.hooks {
		if h.Enabled {
			out = append(out, h)
		}
	}
	return out, nil
}

func (s *queueStore) GetWebhook(_ context.Context, id uuid.UUID) (authstore.WebhookConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hooks[id]
	if !ok {
		return authstore.WebhookConfig{}, fmt.Errorf("webhook: %w", auth.ErrNotFound)
	}
	return h, nil
}

func (s *queueStore) RecordWebhookDelivery(context.Context, uuid.UUID, string, authstore.WebhookDelivery) error {
	return nil
}

func (s *queueStore) RecordWebhookAttempt(_ context.Context, a authstore.WebhookAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, a)
	s.events[a.DeliveryID] = a.Event
	return nil
}

func (s *queueStore) GetWebhookDeliveryEvent(_ context.Context, hookID, deliveryID uuid.UUID) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.attempts {
		if a.DeliveryID == deliveryID && a.HookID == hookID {
			return s.events[deliveryID], nil
		}
	}
	return nil, fmt.Errorf("getting delivery: %w", sql.ErrNoRows)
}

func (s *queueStore) last() authstore.WebhookAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts[len(s.attempts)-1]
}

// receiver is an httptest hook target that answers from a script and keeps
// what it was sent.
type receiver struct {
	srv   *httptest.Server
	mu    sync.Mutex
	codes []int // status per request; the last repeats
	reqs  []http.Header
	n     int
	hook  func(w http.ResponseWriter, r *http.Request) bool // true: handled; set with onRequest
}

// onRequest sets the hook under the receiver's lock: the handler runs on the
// server's goroutines, and -race rejects an unsynchronised write here.
func (rc *receiver) onRequest(f func(w http.ResponseWriter, r *http.Request) bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.hook = f
}

func newReceiver(t *testing.T, codes ...int) *receiver {
	t.Helper()
	rc := &receiver{codes: codes}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.mu.Lock()
		i := min(rc.n, len(rc.codes)-1)
		rc.n++
		rc.reqs = append(rc.reqs, r.Header.Clone())
		code := rc.codes[i]
		hook := rc.hook
		rc.mu.Unlock()
		if hook != nil && hook(w, r) {
			return
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

func (rc *receiver) count() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.n
}

func (rc *receiver) header(i int, name string) string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.reqs[i].Get(name)
}

type queueRig struct {
	store  *fakeOutbox
	hooks  *queueStore
	disp   *WebhookDispatcher
	worker *Worker
	front  *OutboxDispatcher
}

// newQueueRig wires what main wires: a request-side dispatcher that queues,
// a worker, and the webhook dispatcher in queued mode. The worker never waits
// between attempts, so a test is quick; the schedule has its own test.
func newQueueRig(t *testing.T, client *http.Client, hooks ...authstore.WebhookConfig) *queueRig {
	t.Helper()
	outbox := newFakeOutbox()
	hs := newQueueStore(hooks...)
	disp := NewWebhookDispatcher(hs, fixtureBaseURL, quietLog()).
		WithQueue(WebhookQueue{Outbox: outbox, Hooks: hs, History: hs})
	disp.client = client
	w := NewWorker(outbox, map[string]notification.Dispatcher{"webhook": disp}, quietLog())
	w.Backoff = func(int) time.Duration { return time.Second }
	return &queueRig{store: outbox, hooks: hs, disp: disp, worker: w,
		front: NewOutboxDispatcher(outbox, []string{"webhook"}, nil)}
}

// drain runs the worker until nothing is left to send, skipping the waits.
func (r *queueRig) drain(t *testing.T) {
	t.Helper()
	for range 50 {
		r.store.makeDue()
		n, err := r.worker.RunOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			return
		}
	}
	t.Fatal("the worker never ran out of rows")
}

func hookFor(url string) authstore.WebhookConfig {
	return authstore.WebhookConfig{ID: uuid.New(), URL: url, Events: []string{"*"}, Enabled: true,
		Secret: "s3cret", PayloadFormat: "raw"}
}

// Receivers may now see an event more than once, so each delivery names
// itself: the same id on every retry, and the event's name beside it.
func TestQueue_RetryKeepsOneDeliveryIDAndSendsTheHeaders(t *testing.T) {
	rc := newReceiver(t, 503, 503, 200)
	hook := hookFor(rc.srv.URL)
	rig := newQueueRig(t, rc.srv.Client(), hook)

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.drain(t)

	require.Equal(t, 3, rc.count())
	id := rc.header(0, "X-GHD-Delivery")
	_, err := uuid.Parse(id)
	require.NoError(t, err, "X-GHD-Delivery must be a uuid")
	for i := range 3 {
		require.Equal(t, id, rc.header(i, "X-GHD-Delivery"), "attempt %d changed the delivery id", i+1)
		require.Equal(t, "ticket.replied", rc.header(i, "X-GHD-Event"))
		require.NotEmpty(t, rc.header(i, SignatureHeader), "signing must not change")
	}

	last := rig.hooks.last()
	require.Equal(t, id, last.DeliveryID.String())
	require.Equal(t, "", last.Result.Error)
	require.Equal(t, 200, last.Result.Status)
	require.False(t, last.Retrying)
	require.Empty(t, rig.store.failed, "a delivery that got through is not a failed row")
	require.Equal(t, 0, len(liveRows(rig.store)))
}

func liveRows(f *fakeOutbox) []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []uuid.UUID
	for id := range f.rows {
		if f.failed[id] == "" {
			out = append(out, id)
		}
	}
	return out
}

// Two different deliveries, two different ids.
func TestQueue_EachEventIsItsOwnDelivery(t *testing.T) {
	rc := newReceiver(t, 200)
	rig := newQueueRig(t, rc.srv.Client(), hookFor(rc.srv.URL))

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.drain(t)

	require.Equal(t, 2, rc.count())
	require.NotEqual(t, rc.header(0, "X-GHD-Delivery"), rc.header(1, "X-GHD-Delivery"))
}

func TestQueue_RetryDecisions(t *testing.T) {
	for _, tc := range []struct {
		name         string
		codes        []int
		wantRequests int
		wantFailed   bool // the row ran out of attempts
		wantClass    string
	}{
		{"500 is retried until it works", []int{500, 200}, 2, false, ""},
		{"502 is retried", []int{502, 200}, 2, false, ""},
		{"429 is retried", []int{429, 200}, 2, false, ""},
		{"400 is final", []int{400, 200}, 1, false, DeliveryHTTPStatus},
		{"404 is final", []int{404, 200}, 1, false, DeliveryHTTPStatus},
		{"410 is final", []int{410, 200}, 1, false, DeliveryHTTPStatus},
		{"a hook that never recovers stops after 8", []int{500}, 8, true, DeliveryHTTPStatus},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := newReceiver(t, tc.codes...)
			rig := newQueueRig(t, rc.srv.Client(), hookFor(rc.srv.URL))

			require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
			rig.drain(t)

			require.Equal(t, tc.wantRequests, rc.count())
			require.Equal(t, tc.wantFailed, len(rig.store.failed) == 1)
			last := rig.hooks.last()
			require.Equal(t, tc.wantClass, last.Result.Error)
			require.False(t, last.Retrying, "nothing is pending once drained")
		})
	}
}

func TestQueue_ConnectionErrorsAreRetried(t *testing.T) {
	rc := newReceiver(t, 200)
	var first atomic.Bool
	first.Store(true)
	rc.onRequest(func(w http.ResponseWriter, _ *http.Request) bool {
		if !first.CompareAndSwap(true, false) {
			return false
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close() // closed without a response
		}
		return true
	})
	rig := newQueueRig(t, rc.srv.Client(), hookFor(rc.srv.URL))

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.drain(t)

	require.Equal(t, 2, rc.count())
	require.Equal(t, "", rig.hooks.last().Result.Error)
}

func TestQueue_TimeoutsAreRetried(t *testing.T) {
	rc := newReceiver(t, 200)
	var first atomic.Bool
	first.Store(true)
	rc.onRequest(func(w http.ResponseWriter, r *http.Request) bool {
		if !first.CompareAndSwap(true, false) {
			return false
		}
		// Hold the request until the client gives up. The body is read first:
		// the server only notices a closed connection once it has.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		return true
	})
	rig := newQueueRig(t, &http.Client{Timeout: 100 * time.Millisecond}, hookFor(rc.srv.URL))

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.drain(t)

	require.Equal(t, 2, rc.count())
	require.Equal(t, "", rig.hooks.last().Result.Error)
}

// The address guard's refusal is not a hiccup: asking again gets the same
// answer, and repeating it is a pointless knock on an internal address.
func TestQueue_BlockedAddressIsNeverRetried(t *testing.T) {
	rc := newReceiver(t, 200) // on 127.0.0.1, which the guarded client refuses
	rig := newQueueRig(t, safehttp.Client(2*time.Second), hookFor(rc.srv.URL))

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.drain(t)

	require.Equal(t, 0, rc.count(), "the guard must stop the connection")
	require.Len(t, rig.hooks.attempts, 1, "exactly one attempt")
	last := rig.hooks.last()
	require.Equal(t, DeliveryBlockedAddress, last.Result.Error)
	require.False(t, last.Retrying)
	require.Empty(t, rig.store.failed)
}

// 8 attempts in all, on the email schedule, then the row is given up on with
// a reason that names a class and not the address.
func TestQueue_GivesUpAfterEightAndKeepsNoURL(t *testing.T) {
	rc := newReceiver(t, 500)
	hook := hookFor(rc.srv.URL + "/token-abc")
	rig := newQueueRig(t, rc.srv.Client(), hook)
	rig.worker.Backoff = defaultBackoff

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	// Run by hand: the schedule is the point here.
	for range 20 {
		rig.store.makeDue()
		n, err := rig.worker.RunOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}
	require.Equal(t, 8, rc.count())
	require.Len(t, rig.store.failed, 1)
	for _, reason := range rig.store.failed {
		require.NotContains(t, reason, "token-abc")
		require.NotContains(t, reason, rc.srv.URL)
	}
	for _, e := range rig.store.lastErrors {
		require.NotContains(t, e, "token-abc")
	}
}

// A receiver that asks to be left alone for a while is obeyed, but never for
// longer than the longest wait in the schedule.
func TestWorker_HonoursRetryAfterWithinTheCap(t *testing.T) {
	for _, tc := range []struct {
		name    string
		after   time.Duration
		backoff time.Duration
		want    time.Duration
	}{
		{"longer than the schedule", 90 * time.Second, 30 * time.Second, 90 * time.Second},
		{"shorter than the schedule does not hurry it", 5 * time.Second, 30 * time.Second, 30 * time.Second},
		{"capped at an hour", 48 * time.Hour, 30 * time.Second, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeOutbox()
			ch := &recorder{fail: &notification.RetryAfterError{Err: errors.New("busy"), After: tc.after}}
			w := newTestWorker(store, map[string]notification.Dispatcher{"webhook": ch})
			w.Backoff = func(int) time.Duration { return tc.backoff }
			require.NoError(t, NewOutboxDispatcher(store, []string{"webhook"}, nil).Dispatch(context.Background(), fixtureReplyEvent(false)))

			_, err := w.RunOnce(context.Background())
			require.NoError(t, err)

			require.Len(t, store.retries, 1)
			for _, at := range store.retries {
				require.InDelta(t, tc.want.Seconds(), time.Until(at).Seconds(), 2)
			}
		})
	}
}

func TestRetryAfterHeader_IsReadAsSecondsOrADate(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"120", 120 * time.Second},
		{"0", 0},
		{"", 0},
		{"soon", 0},
		{"-5", 0},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0},
	} {
		require.Equal(t, tc.want, parseRetryAfter(tc.in, now), "Retry-After %q", tc.in)
	}
}

func TestQueue_429RetryAfterReachesTheWorker(t *testing.T) {
	rc := newReceiver(t, 429, 200)
	rc.onRequest(func(w http.ResponseWriter, _ *http.Request) bool {
		if rc.count() == 1 {
			w.Header().Set("Retry-After", "120")
		}
		return false
	})
	rig := newQueueRig(t, rc.srv.Client(), hookFor(rc.srv.URL))

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	_, err := rig.worker.RunOnce(context.Background()) // the fan-out
	require.NoError(t, err)
	_, err = rig.worker.RunOnce(context.Background()) // the first attempt
	require.NoError(t, err)

	require.Len(t, rig.store.retries, 1)
	for _, at := range rig.store.retries {
		require.InDelta(t, 120, time.Until(at).Seconds(), 3, "the 1 s schedule must give way to Retry-After")
	}
	require.True(t, rig.hooks.last().Retrying)
}

// One hook failing does not hold up or repeat another.
func TestQueue_HooksAreRetriedIndependently(t *testing.T) {
	good := newReceiver(t, 200)
	bad := newReceiver(t, 500, 500, 200)
	rig := newQueueRig(t, good.srv.Client(), hookFor(good.srv.URL), hookFor(bad.srv.URL))

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.drain(t)

	require.Equal(t, 1, good.count(), "the healthy hook must not be sent again because another failed")
	require.Equal(t, 3, bad.count())
}

// A crash between queueing the per-hook rows and settling the fan-out row
// runs the fan-out again; the rows it queues must be the same rows.
func TestFanOut_IsRepeatable(t *testing.T) {
	rc := newReceiver(t, 200)
	rig := newQueueRig(t, rc.srv.Client(), hookFor(rc.srv.URL), hookFor(rc.srv.URL+"/b"))

	ctx := context.WithValue(context.Background(), rowKey{}, uuid.New())
	require.NoError(t, rig.disp.Dispatch(ctx, fixtureReplyEvent(false)))
	first := append([]uuid.UUID(nil), rig.store.order...)
	require.NoError(t, rig.disp.Dispatch(ctx, fixtureReplyEvent(false)))

	require.Len(t, first, 2)
	require.Equal(t, first, rig.store.order[:2])
	require.ElementsMatch(t, first, uniq(rig.store.order), "a repeat queued different rows")
}

func uniq(in []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, id := range in {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// The copy kept for Redeliver holds ids and nothing else: no address in the
// typed field or in Payload, and no subject, tracking number or reply text.
func TestFanOut_StoredEventHoldsNoAddressOrContent(t *testing.T) {
	rc := newReceiver(t, 200)
	rig := newQueueRig(t, rc.srv.Client(), hookFor(rc.srv.URL))
	ev := contentEvent()

	require.NoError(t, rig.front.Dispatch(context.Background(), ev))
	rig.drain(t)

	require.Equal(t, 1, rc.count())
	stored := string(rig.hooks.last().Event)
	for _, leak := range contentStrings {
		require.NotContains(t, stored, leak)
	}
	require.NotContains(t, stored, "guest_link")
	require.Contains(t, stored, ev.TicketID.String(), "ids stay")
}

func TestQueue_DisabledOrDeletedHookGetsNothing(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := hookFor(rc.srv.URL)
	rig := newQueueRig(t, rc.srv.Client(), hook)

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	_, err := rig.worker.RunOnce(context.Background()) // fan-out: one row for the hook
	require.NoError(t, err)

	rig.hooks.mu.Lock()
	h := rig.hooks.hooks[hook.ID]
	h.Enabled = false
	rig.hooks.hooks[hook.ID] = h
	rig.hooks.mu.Unlock()
	rig.drain(t)
	require.Equal(t, 0, rc.count(), "disabled between queueing and sending")

	rig.hooks.mu.Lock()
	h.Enabled = true
	rig.hooks.hooks[hook.ID] = h
	rig.hooks.mu.Unlock()
	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	_, err = rig.worker.RunOnce(context.Background()) // fan-out again
	require.NoError(t, err)
	rig.hooks.mu.Lock()
	delete(rig.hooks.hooks, hook.ID)
	rig.hooks.mu.Unlock()
	rig.drain(t)
	require.Equal(t, 0, rc.count())
	require.Empty(t, rig.store.failed, "a gone hook settles the row; it is not a failure")
}

// Hook selection stays in one place, so a filter on ticket attributes (#413)
// has a single seam to join.
func TestQueue_OnlySubscribedHooksAreQueued(t *testing.T) {
	rc := newReceiver(t, 200)
	repliesOnly := hookFor(rc.srv.URL)
	repliesOnly.Events = []string{"ticket.replied"}
	notesOnly := hookFor(rc.srv.URL + "/notes")
	notesOnly.Events = []string{"ticket.note_added"}
	rig := newQueueRig(t, rc.srv.Client(), repliesOnly, notesOnly)

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.drain(t)
	require.Equal(t, 1, rc.count())
	require.Equal(t, repliesOnly.ID, rig.hooks.last().HookID)

	note := fixtureReplyEvent(true)
	note.Type = notification.EventTicketNoteAdded
	require.NoError(t, rig.front.Dispatch(context.Background(), note))
	rig.drain(t)
	require.Equal(t, 2, rc.count())
	require.Equal(t, notesOnly.ID, rig.hooks.last().HookID)
	require.Equal(t, "ticket.note_added", rc.header(1, "X-GHD-Event"))
}

// Slack, Teams, Discord and JIRA hooks take the same road and keep their
// built bodies.
func TestQueue_ChatFormatsStillGetTheirOwnBody(t *testing.T) {
	var bodies []string
	var mu sync.Mutex
	rc := newReceiver(t, 200)
	rc.onRequest(func(w http.ResponseWriter, r *http.Request) bool {
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		mu.Lock()
		bodies = append(bodies, string(b[:n]))
		mu.Unlock()
		return false
	})
	var hooks []authstore.WebhookConfig
	for _, f := range Formats {
		h := hookFor(rc.srv.URL + "/" + string(f))
		h.PayloadFormat = string(f)
		hooks = append(hooks, h)
	}
	rig := newQueueRig(t, rc.srv.Client(), hooks...)
	note := fixtureReplyEvent(true)
	note.Type = notification.EventTicketNoteAdded

	require.NoError(t, rig.front.Dispatch(context.Background(), note))
	rig.drain(t)

	require.Equal(t, len(Formats), rc.count())
	mu.Lock()
	defer mu.Unlock()
	var sawText, sawRaw int
	for _, b := range bodies {
		if strings.Contains(b, "Internal note added") {
			sawText++
		}
		if strings.Contains(b, `"Type":"ticket.note_added"`) {
			sawRaw++
		}
	}
	require.Equal(t, 1, sawRaw, "raw keeps its shape")
	require.Equal(t, 3, sawText, "slack, teams and discord say what happened (jira carries a flag)")
}

// ── Redeliver ────────────────────────────────────────────────────────────────

func TestRedeliver_SendsTheStoredEventAsANewDelivery(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := hookFor(rc.srv.URL)
	rig := newQueueRig(t, rc.srv.Client(), hook)
	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.drain(t)
	original := rig.hooks.last().DeliveryID

	require.NoError(t, rig.disp.Redeliver(context.Background(), hook.ID, original))
	rig.drain(t)

	require.Equal(t, 2, rc.count())
	require.NotEqual(t, rc.header(0, "X-GHD-Delivery"), rc.header(1, "X-GHD-Delivery"),
		"a receiver that de-duplicates by id would drop a replay under the old one")
	require.Equal(t, "ticket.replied", rc.header(1, "X-GHD-Event"))
	require.Equal(t, 200, rig.hooks.last().Result.Status)
}

func TestRedeliver_Refusals(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := hookFor(rc.srv.URL)
	rig := newQueueRig(t, rc.srv.Client(), hook)
	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.drain(t)
	delivery := rig.hooks.last().DeliveryID

	require.ErrorIs(t, rig.disp.Redeliver(context.Background(), hook.ID, uuid.New()), ErrDeliveryNotFound)
	require.ErrorIs(t, rig.disp.Redeliver(context.Background(), uuid.New(), delivery), ErrWebhookNotFound)

	rig.hooks.mu.Lock()
	h := rig.hooks.hooks[hook.ID]
	h.Enabled = false
	rig.hooks.hooks[hook.ID] = h
	rig.hooks.mu.Unlock()
	require.ErrorIs(t, rig.disp.Redeliver(context.Background(), hook.ID, delivery), ErrWebhookDisabled)

	require.Equal(t, 1, rc.count(), "none of the refusals may send")
}

// ── Legacy path ──────────────────────────────────────────────────────────────

// A dispatcher built without a queue still sends at once, and still names the
// delivery: the headers do not depend on the queue.
func TestDispatch_WithoutAQueueStillSendsTheHeaders(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := hookFor(rc.srv.URL)
	disp := NewWebhookDispatcher(fakeWebhookStore{hook}, fixtureBaseURL, quietLog())
	disp.client = rc.srv.Client()

	require.NoError(t, disp.Dispatch(context.Background(), fixtureReplyEvent(false)))
	require.Eventually(t, func() bool { return rc.count() == 1 }, 2*time.Second, 10*time.Millisecond)

	_, err := uuid.Parse(rc.header(0, "X-GHD-Delivery"))
	require.NoError(t, err)
	require.Equal(t, "ticket.replied", rc.header(0, "X-GHD-Event"))
}
