package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// fakeOutbox is an in-memory notification.Outbox.
type fakeOutbox struct {
	mu      sync.Mutex
	rows    map[uuid.UUID]*fakeRow
	order   []uuid.UUID
	retries map[uuid.UUID]time.Time
	failed  map[uuid.UUID]string
	// lastErrors is every error text passed to Retry, as last_error stores it.
	lastErrors []string
}

type fakeRow struct {
	channel  string
	event    []byte
	attempts int
	due      time.Time
}

func newFakeOutbox() *fakeOutbox {
	return &fakeOutbox{rows: map[uuid.UUID]*fakeRow{}, retries: map[uuid.UUID]time.Time{}, failed: map[uuid.UUID]string{}}
}

func (f *fakeOutbox) Enqueue(_ context.Context, id uuid.UUID, ch string, ev []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[id] = &fakeRow{channel: ch, event: ev}
	f.order = append(f.order, id)
	return nil
}

func (f *fakeOutbox) Claim(_ context.Context, limit int, _ time.Duration) ([]notification.OutboxRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []notification.OutboxRow
	for _, id := range f.order {
		r, ok := f.rows[id]
		if !ok || f.failed[id] != "" || r.due.After(time.Now()) || len(out) == limit {
			continue
		}
		r.attempts++
		r.due = time.Now().Add(time.Hour) // leased
		out = append(out, notification.OutboxRow{ID: id, Channel: r.channel, Event: r.event, Attempts: r.attempts})
	}
	return out, nil
}

func (f *fakeOutbox) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, id)
	return nil
}

func (f *fakeOutbox) Retry(_ context.Context, id uuid.UUID, at time.Time, lastErr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastErrors = append(f.lastErrors, lastErr)
	f.rows[id].due = at
	f.retries[id] = at
	return nil
}

func (f *fakeOutbox) Fail(_ context.Context, id uuid.UUID, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed[id] = reason
	return nil
}

func (f *fakeOutbox) DeleteFailedBefore(context.Context, time.Time) (int64, error) { return 0, nil }

// makeDue lets a test skip a backoff.
func (f *fakeOutbox) makeDue() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		r.due = time.Time{}
	}
}

func (f *fakeOutbox) channelsQueued() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, id := range f.order {
		if r, ok := f.rows[id]; ok {
			out = append(out, r.channel)
		}
	}
	return out
}

// recorder is a channel that records what it was given and fails on demand.
type recorder struct {
	mu   sync.Mutex
	got  []notification.Event
	fail error
}

func (r *recorder) Dispatch(_ context.Context, ev notification.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, ev)
	return r.fail
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func sampleEvent() notification.Event {
	actor := uuid.New()
	return notification.Event{
		Type:     notification.EventTicketLinked,
		TicketID: uuid.New(),
		ActorID:  &actor,
		Payload: map[string]any{
			"TrackingNumber": "TKT-00042",
			"ReplyBody":      "hello",
			"internal":       false,
			"target_id":      uuid.New(), // a uuid.UUID going in, a string coming out
			"link_type":      "related",
			"Priority":       "high",
		},
		// UTC, as an event read back from the outbox is: a time.Local value
		// compares unequal to the same instant in UTC, so with Local the test
		// passed on a developer's machine and failed on a UTC CI runner.
		OccurredAt:     time.Now().UTC().Truncate(time.Microsecond),
		TrackingNumber: "TKT-00042",
		Recipient:      "guest@example.com",
		Subject:        "Printer on fire",
		StatusName:     "Open",
		GuestLink:      true,
	}
}

// A request must not wait on any channel: queueing writes rows and returns.
func TestOutboxDispatcher_QueuesOneRowPerChannelAndSendsNothing(t *testing.T) {
	store := newFakeOutbox()
	woken := 0
	d := NewOutboxDispatcher(store, []string{"email", "webhook"}, func() { woken++ })

	require.NoError(t, d.Dispatch(context.Background(), sampleEvent()))
	require.ElementsMatch(t, []string{"email", "webhook"}, store.channelsQueued())
	require.Equal(t, 1, woken)
}

// The guest token table holds only hashes. A raw token in the outbox would
// be a working credential at rest.
func TestOutboxDispatcher_RefusesARawGuestToken(t *testing.T) {
	store := newFakeOutbox()
	d := NewOutboxDispatcher(store, []string{"email"}, nil)
	ev := sampleEvent()
	ev.GuestToken = "raw-token-value"

	require.ErrorIs(t, d.Dispatch(context.Background(), ev), ErrRawGuestToken)
	require.Empty(t, store.channelsQueued())
}

// A queued event must reach each channel as the channel would have seen it
// without the queue: the email fields intact, and the webhook body unchanged.
func TestOutboxRecord_RoundTripsWhatChannelsRead(t *testing.T) {
	ev := sampleEvent()
	b, err := encodeEvent(ev)
	require.NoError(t, err)
	got, err := decodeEvent(b)
	require.NoError(t, err)

	require.Equal(t, ev.Type, got.Type)
	require.Equal(t, ev.TicketID, got.TicketID)
	require.Equal(t, ev.ActorID, got.ActorID)
	require.True(t, ev.OccurredAt.Equal(got.OccurredAt))
	require.Equal(t, ev.TrackingNumber, got.TrackingNumber)
	require.Equal(t, ev.Recipient, got.Recipient)
	require.Equal(t, ev.Subject, got.Subject)
	require.Equal(t, ev.StatusName, got.StatusName)
	require.True(t, got.GuestLink, "the send-time step would not run")

	// What the chat and ITSM formatters render, and the raw webhook body.
	require.Equal(t, summarize(ev, "https://desk.example"), summarize(got, "https://desk.example"))
	before, err := json.Marshal(ev)
	require.NoError(t, err)
	after, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
}

// Every field of Event is either stored or deliberately left out. A new field
// added to Event without a decision would otherwise vanish silently on the
// way through the queue.
func TestOutboxRecord_CoversEveryEventField(t *testing.T) {
	stored := map[string]bool{}
	rt := reflect.TypeOf(outboxRecord{})
	for i := range rt.NumField() {
		stored[rt.Field(i).Name] = true
	}
	notStored := map[string]string{
		"GuestToken": "a raw credential; created by the worker at send time instead",
	}
	et := reflect.TypeOf(notification.Event{})
	for i := range et.NumField() {
		name := et.Field(i).Name
		if stored[name] {
			continue
		}
		_, decided := notStored[name]
		require.True(t, decided, "Event.%s is neither stored in outboxRecord nor listed as deliberately left out", name)
	}
}

func newTestWorker(store notification.Outbox, channels map[string]notification.Dispatcher) *Worker {
	w := NewWorker(store, channels, quietLog())
	w.MaxAttempts = 3
	return w
}

// Delivered rows are settled and not sent again.
func TestWorker_DeliversAndSettles(t *testing.T) {
	store := newFakeOutbox()
	email := &recorder{}
	require.NoError(t, NewOutboxDispatcher(store, []string{"email"}, nil).Dispatch(context.Background(), sampleEvent()))

	w := newTestWorker(store, map[string]notification.Dispatcher{"email": email})
	n, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, email.count())
	require.Empty(t, store.channelsQueued(), "a delivered row was left queued")

	store.makeDue()
	_, err = w.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, email.count(), "a delivered row was sent twice")
}

// A failed send is retried later, not dropped and not retried at once; after
// the last attempt it is given up on rather than retried forever.
func TestWorker_RetriesWithBackoffThenFails(t *testing.T) {
	store := newFakeOutbox()
	email := &recorder{fail: errors.New("smtp: connection refused")}
	require.NoError(t, NewOutboxDispatcher(store, []string{"email"}, nil).Dispatch(context.Background(), sampleEvent()))
	w := newTestWorker(store, map[string]notification.Dispatcher{"email": email})

	_, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, store.retries, 1, "a failed send was not rescheduled")
	for _, at := range store.retries {
		require.True(t, at.After(time.Now().Add(20*time.Second)), "retried without backing off")
	}
	_, err = w.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, email.count(), "retried before its backoff was due")

	for range w.MaxAttempts - 1 {
		store.makeDue()
		_, err = w.RunOnce(context.Background())
		require.NoError(t, err)
	}
	require.Equal(t, w.MaxAttempts, email.count())
	require.Len(t, store.failed, 1, "not given up on after the last attempt")

	store.makeDue()
	_, err = w.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, w.MaxAttempts, email.count(), "a failed row was sent again")
}

// One row per channel: the channel that failed is retried on its own, and
// the one that succeeded is not sent twice.
func TestWorker_AFailingChannelDoesNotResendTheOther(t *testing.T) {
	store := newFakeOutbox()
	email := &recorder{fail: errors.New("smtp down")}
	webhook := &recorder{}
	require.NoError(t, NewOutboxDispatcher(store, []string{"email", "webhook"}, nil).Dispatch(context.Background(), sampleEvent()))
	w := newTestWorker(store, map[string]notification.Dispatcher{"email": email, "webhook": webhook})

	_, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	email.fail = nil
	store.makeDue()
	_, err = w.RunOnce(context.Background())
	require.NoError(t, err)

	require.Equal(t, 2, email.count())
	require.Equal(t, 1, webhook.count(), "the channel that succeeded was sent again")
	require.Empty(t, store.channelsQueued())
}

func TestWorker_UnknownChannelIsFailedNotRetried(t *testing.T) {
	store := newFakeOutbox()
	require.NoError(t, NewOutboxDispatcher(store, []string{"pager"}, nil).Dispatch(context.Background(), sampleEvent()))
	w := newTestWorker(store, map[string]notification.Dispatcher{})

	_, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, store.failed, 1)
	require.Empty(t, store.retries)
}

func TestDefaultBackoff(t *testing.T) {
	require.Equal(t, 30*time.Second, defaultBackoff(1))
	require.Equal(t, time.Minute, defaultBackoff(2))
	require.Equal(t, 4*time.Minute, defaultBackoff(4))
	require.Equal(t, time.Hour, defaultBackoff(20))
}

// The send-time step runs only for events marked GuestLink, sends what it
// returns, sends nothing when nobody is left to send to, and leaves a failure
// to the worker's retry.
func TestGuestLinkDispatcher(t *testing.T) {
	ctx := context.Background()
	plain := sampleEvent()
	plain.GuestLink = false

	t.Run("an event without a guest link passes straight through", func(t *testing.T) {
		next := &recorder{}
		called := false
		d := NewGuestLinkDispatcher(next, func(context.Context, notification.Event) (notification.Event, bool, error) {
			called = true
			return notification.Event{}, false, nil
		})
		require.NoError(t, d.Dispatch(ctx, plain))
		require.False(t, called)
		require.Equal(t, 1, next.count())
	})
	t.Run("sends the event the step returns", func(t *testing.T) {
		next := &recorder{}
		d := NewGuestLinkDispatcher(next, func(_ context.Context, ev notification.Event) (notification.Event, bool, error) {
			ev.GuestToken = "minted-at-send"
			return ev, true, nil
		})
		require.NoError(t, d.Dispatch(ctx, sampleEvent()))
		require.Equal(t, "minted-at-send", next.got[0].GuestToken)
	})
	t.Run("nobody to send to: nothing sent, settled", func(t *testing.T) {
		next := &recorder{}
		d := NewGuestLinkDispatcher(next, func(_ context.Context, ev notification.Event) (notification.Event, bool, error) {
			return ev, false, nil
		})
		require.NoError(t, d.Dispatch(ctx, sampleEvent()))
		require.Zero(t, next.count())
	})
	t.Run("a failure is returned for the worker to retry", func(t *testing.T) {
		next := &recorder{}
		boom := errors.New("db down")
		d := NewGuestLinkDispatcher(next, func(_ context.Context, ev notification.Event) (notification.Event, bool, error) {
			return ev, false, boom
		})
		require.ErrorIs(t, d.Dispatch(ctx, sampleEvent()), boom)
		require.Zero(t, next.count())
	})
}

// A lease shorter than a batch hands rows to another replica while this
// worker still means to send them: duplicates under a slow relay. The
// defaults must leave room, and a configuration that does not is reduced.
func TestWorker_ABatchFitsInsideItsLease(t *testing.T) {
	w := NewWorker(newFakeOutbox(), nil, quietLog())
	perSend := w.SendTimeout + w.ChannelOverrun
	require.Greater(t, w.Lease, time.Duration(w.Batch)*perSend,
		"the default batch can outlast its lease")
	// The email channel keeps its own SMTP limits (20 s to dial, 20 s on the
	// connection) and ignores the context, so the overrun must cover them.
	require.GreaterOrEqual(t, w.ChannelOverrun, 40*time.Second)
	require.Equal(t, w.Batch, w.safeBatch())

	w.Batch, w.Lease, w.SendTimeout, w.ChannelOverrun = 20, 5*time.Minute, time.Minute, 0
	require.LessOrEqual(t, time.Duration(w.safeBatch())*w.SendTimeout, w.Lease-w.SendTimeout)
	w.Lease = 30 * time.Second
	require.Equal(t, 1, w.safeBatch(), "never below one row")

	// The overrun counts: 5 minutes / (1 m + 40 s) leaves room for 2 rows,
	// where SendTimeout alone would allow 4 (#351). With the defaults both
	// arithmetics give 4, so only a case like this shows the difference.
	w.Batch, w.Lease, w.SendTimeout, w.ChannelOverrun = 20, 5*time.Minute, time.Minute, 40*time.Second
	require.Equal(t, 2, w.safeBatch(), "the channel overrun was left out of each send's time")
}

// guest.link_resent is never delivered to webhooks, so it must not queue a
// webhook row: every unauthenticated resend request wrote one, for nothing.
func TestOutboxDispatcher_QueuesOnlyForChannelsThatCarryTheEvent(t *testing.T) {
	store := newFakeOutbox()
	d := NewOutboxDispatcher(store, []string{"email", "webhook"}, nil)
	ev := sampleEvent()
	ev.Type = notification.EventGuestLinkResent
	require.NoError(t, d.Dispatch(context.Background(), ev))
	require.Equal(t, []string{"email"}, store.channelsQueued())
}

type panics struct{}

func (panics) Dispatch(context.Context, notification.Event) error { panic("formatter bug") }

// A send that panics fails its row rather than crashing the worker, and with
// it the server, every time the row is claimed again.
func TestWorker_APanickingSendFailsItsRow(t *testing.T) {
	store := newFakeOutbox()
	require.NoError(t, NewOutboxDispatcher(store, []string{"email"}, nil).Dispatch(context.Background(), sampleEvent()))
	w := newTestWorker(store, map[string]notification.Dispatcher{"email": panics{}})
	require.NotPanics(t, func() {
		_, err := w.RunOnce(context.Background())
		require.NoError(t, err)
	})
	require.Len(t, store.failed, 1)
}

// No email row for an event with nobody to mail (an account holder's status
// change), but a guest-link event without a recipient yet is kept: the
// send-time step fills the recipient in.
func TestOutboxDispatcher_NoEmailRowWithNobodyToMail(t *testing.T) {
	store := newFakeOutbox()
	d := NewOutboxDispatcher(store, []string{"email"}, nil)
	ev := sampleEvent()
	ev.Recipient, ev.GuestLink = "", false
	require.NoError(t, d.Dispatch(context.Background(), ev))
	require.Empty(t, store.channelsQueued())

	ev.GuestLink = true
	require.NoError(t, d.Dispatch(context.Background(), ev))
	require.Equal(t, []string{"email"}, store.channelsQueued())

	// And the ordinary case: an account holder's reply has a recipient and no
	// guest link. Round 3 changed the filter to GuestLink alone and every test
	// passed, silently dropping every account holder's reply mail.
	store2 := newFakeOutbox()
	ev.Recipient, ev.GuestLink = "reporter@example.com", false
	require.NoError(t, NewOutboxDispatcher(store2, []string{"email"}, nil).Dispatch(context.Background(), ev))
	require.Equal(t, []string{"email"}, store2.channelsQueued(), "an account holder's mail was not queued")
}

// The production loop: drains more rows than one batch, wakes on demand,
// stops when cancelled, and reduces a batch that does not fit its lease.
func TestWorker_RunDrainsWakesAndStops(t *testing.T) {
	store := newFakeOutbox()
	email := &recorder{}
	d := NewOutboxDispatcher(store, []string{"email"}, nil)
	for range 12 {
		require.NoError(t, d.Dispatch(context.Background(), sampleEvent()))
	}
	w := newTestWorker(store, map[string]notification.Dispatcher{"email": email})
	w.Poll = time.Hour // only a wake or the first pass may deliver
	w.Batch, w.Lease, w.SendTimeout, w.ChannelOverrun = 50, time.Minute, 10*time.Second, 0

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	require.Eventually(t, func() bool { return email.count() == 12 }, 5*time.Second, 10*time.Millisecond,
		"the first pass did not drain every row across batches")
	require.Equal(t, 5, w.Batch, "the batch was not reduced to fit the lease")

	require.NoError(t, d.Dispatch(context.Background(), sampleEvent()))
	w.Wake()
	require.Eventually(t, func() bool { return email.count() == 13 }, 5*time.Second, 10*time.Millisecond,
		"a wake did not deliver")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop when cancelled")
	}
}

// cancelAwareOutbox is a fakeOutbox whose Claim fails once its context is
// cancelled, as the real store's query does.
type cancelAwareOutbox struct{ *fakeOutbox }

func (c cancelAwareOutbox) Claim(ctx context.Context, limit int, lease time.Duration) ([]notification.OutboxRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.fakeOutbox.Claim(ctx, limit, lease)
}

// cancelOnSend cancels the worker's context from inside a send, so the next
// claim in the same drain runs on a cancelled context.
type cancelOnSend struct{ cancel context.CancelFunc }

func (c cancelOnSend) Dispatch(context.Context, notification.Event) error { c.cancel(); return nil }

// A claim cut off by shutdown is not an error worth reporting (#351): only
// the log shows the difference, so the log is what this reads.
func TestWorker_ShutdownMidDrainLogsNoClaimError(t *testing.T) {
	store := cancelAwareOutbox{newFakeOutbox()}
	d := NewOutboxDispatcher(store, []string{"email"}, nil)
	for range 3 {
		require.NoError(t, d.Dispatch(context.Background(), sampleEvent()))
	}
	ctx, cancel := context.WithCancel(context.Background())
	var logged bytes.Buffer
	w := NewWorker(store, map[string]notification.Dispatcher{"email": cancelOnSend{cancel}},
		slog.New(slog.NewTextHandler(&logged, nil)))
	// A full batch, so Run claims again after it, on the now-cancelled context.
	w.Batch, w.Lease, w.SendTimeout, w.ChannelOverrun = 1, time.Hour, time.Minute, 0

	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	require.NotContains(t, logged.String(), "claim failed", "shutdown was logged as a claim error")
}

// The stored time keeps its instant whatever zone it was written in. The
// round-trip test above uses UTC so it passes on every machine; this one uses
// a fixed non-UTC zone, so an encoder that dropped the zone and read the wall
// clock as UTC would be caught on every machine (#351).
func TestOutboxRecord_KeepsTheInstantInAnyZone(t *testing.T) {
	ev := sampleEvent()
	ev.OccurredAt = time.Date(2026, 3, 8, 1, 30, 0, 0, time.FixedZone("UTC-6", -6*60*60))
	b, err := encodeEvent(ev)
	require.NoError(t, err)
	got, err := decodeEvent(b)
	require.NoError(t, err)
	require.True(t, ev.OccurredAt.Equal(got.OccurredAt), "stored %v, read back %v", ev.OccurredAt, got.OccurredAt)
}

// #350: a mail server's rejection usually names the recipient, and the
// worker keeps the error text in last_error (30 days on a failed row) and in
// the log. The address is redacted from both; the SMTP status is what an
// operator needs, and it stays.
func TestWorker_RedactsAddressesFromStoredAndLoggedErrors(t *testing.T) {
	store := newFakeOutbox()
	reject := &recorder{fail: errors.New("SMTP RCPT TO: 550 5.1.1 <guest@example.com>: Recipient address rejected; also cc Ada.Lovelace+tickets@sub.example.co.uk")}
	require.NoError(t, NewOutboxDispatcher(store, []string{"email"}, nil).Dispatch(context.Background(), sampleEvent()))
	var logged bytes.Buffer
	w := NewWorker(store, map[string]notification.Dispatcher{"email": reject}, slog.New(slog.NewTextHandler(&logged, nil)))
	w.MaxAttempts = 2

	_, err := w.RunOnce(context.Background()) // attempt 1: rescheduled
	require.NoError(t, err)
	store.makeDue()
	_, err = w.RunOnce(context.Background()) // attempt 2: given up on
	require.NoError(t, err)

	require.Len(t, store.failed, 1)
	for _, reason := range store.failed {
		require.NotContains(t, reason, "guest@example.com")
		require.NotContains(t, reason, "Ada.Lovelace")
		require.Contains(t, reason, "550 5.1.1", "the SMTP status was redacted too")
	}
	for _, lastErr := range store.lastErrors {
		require.NotContains(t, lastErr, "@example.com")
	}
	require.NotContains(t, logged.String(), "guest@example.com")
	require.NotContains(t, logged.String(), "Ada.Lovelace")
}

var redactAddressCases = []struct{ in, want string }{
	{"550 5.1.1 <guest@example.com>: Recipient address rejected", "550 5.1.1 <[address]>: Recipient address rejected"},
	{"RCPT TO:<a.b+c@sub.example.co.uk> denied", "RCPT TO:<[address]> denied"},
	{"two: x@a.test, y@b.test", "two: [address], [address]"},
	// #358: a quoted local part and an IP-literal domain are legal
	// addresses (user.ValidateEmail accepts both), so a guest can have one.
	{`550 5.1.1 <"john doe"@example.com>: Recipient address rejected`, "550 5.1.1 <[address]>: Recipient address rejected"},
	{`550 5.1.1 <"john \"jj\" doe"@example.com> rejected`, "550 5.1.1 <[address]> rejected"},
	{"550 5.1.1 <guest@[192.168.1.10]>: Recipient address rejected", "550 5.1.1 <[address]>: Recipient address rejected"},
	{"550 5.1.1 <guest@[IPv6:2001:db8::1]>: Recipient address rejected", "550 5.1.1 <[address]>: Recipient address rejected"},
	// A queue id shaped like an address is hidden too; nothing is lost
	// that an operator needs.
	{"250 2.0.0 Ok: queued as 4ABC123@mail.example.com", "250 2.0.0 Ok: queued as [address]"},
	// Ordinary errors are left exactly as they are.
	{"dial tcp 10.0.0.25:587: i/o timeout", "dial tcp 10.0.0.25:587: i/o timeout"},
	{"421 4.7.0 Try again later", "421 4.7.0 Try again later"},
	{"dial tcp [2001:db8::1]:587: connect: connection refused", "dial tcp [2001:db8::1]:587: connect: connection refused"},
	{`unknown channel "pager"`, `unknown channel "pager"`},
	{"panic: runtime error: index out of range [3] with length 2", "panic: runtime error: index out of range [3] with length 2"},
	{"", ""},
}

func TestRedactAddresses(t *testing.T) {
	for _, tc := range redactAddressCases {
		t.Run(tc.in, func(t *testing.T) {
			require.Equal(t, tc.want, redactAddresses(tc.in))
		})
	}
}

// redactAddresses runs on the outbox worker goroutine, and its input is the
// SMTP relay's reply, which the relay controls. Go's regexp is linear in the
// input only when the pattern is bounded: an unbounded quoted local part
// that never closes made matching quadratic, about a minute at 96 KB before
// the bound (measured, #358 review).
//
// The bound is checked against a time limit, not a ratio. Ratios between
// sizes were tried and are not robust: under CPU contention the larger run
// spans more scheduler slices, so a linear matcher can measure 9 to 16 times
// its smaller run, which is the range a quadratic one reaches. The limit is
// far from both sides instead. At these sizes the bounded pattern takes about
// 0.1 to 0.2 s without -race and 3 to 5 s with it, so the limit has more than
// 6 times the race-detector cost of the good case, and more than 100 times
// without -race. The unbounded pattern took 57 to 61 s at these sizes, so a
// 30 s limit fails it, and fails it fast, rather than after a minute.
func TestRedactAddresses_IsLinear(t *testing.T) {
	const limit = 30 * time.Second
	shapes := []struct{ name, in string }{
		// Mutation M7 (no {0,64} on the quoted local part) is caught here.
		{"unclosed quoted local part", `"` + strings.Repeat(`a@b \"`, 16<<10)},
		// Whitespace stops the IP literal early.
		{"unclosed IP literal", strings.Repeat(`a@b x@[`, 16<<10)},
		// The costliest shape per byte. No whitespace, so only the {0,255}
		// bound limits the IP literal. Measured: this stays linear with or
		// without that bound (up to 128 KB), so it guards this input against
		// a regression, but it does not detect removing {0,255}.
		{"unclosed IP literal, no whitespace", strings.Repeat(`a@[`, 32<<10)},
	}
	for _, s := range shapes {
		t.Run(s.name, func(t *testing.T) {
			done := make(chan struct{}, 1) // buffered: a run that finishes after the timeout must not block
			start := time.Now()
			go func() {
				redactAddresses(s.in)
				done <- struct{}{}
			}()
			select {
			case <-done:
				t.Logf("%d bytes: %v", len(s.in), time.Since(start))
			case <-time.After(limit):
				t.Fatalf("redaction of %d bytes did not finish within %v: the pattern is not linear", len(s.in), limit)
			}
		})
	}
}

// Redaction is applied once by design (TestWorker_RedactsTheRawErrorExactlyOnce).
// It is not idempotent in general: a second pass can see the [address] the
// first pass inserted and hide text after it. This pins only the documented
// rows, which a second pass leaves unchanged.
func TestRedactAddresses_TableRowsAreStableUnderASecondPass(t *testing.T) {
	for _, tc := range redactAddressCases {
		once := redactAddresses(tc.in)
		require.Equal(t, once, redactAddresses(once), "input %q", tc.in)
	}
}

// fail redacts the reason itself, not only deliver (#358). A panic's value and
// an undecodable event reach fail without passing deliver's redaction, so
// only a direct call shows the second one is there.
func TestWorker_FailRedactsItsOwnReason(t *testing.T) {
	store := newFakeOutbox()
	var logged bytes.Buffer
	w := NewWorker(store, nil, slog.New(slog.NewTextHandler(&logged, nil)))
	row := notification.OutboxRow{ID: uuid.New(), Channel: "email", Attempts: 1}

	w.fail(context.Background(), row, "panic: x@y.test")

	require.Equal(t, "panic: [address]", store.failed[row.ID])
	require.NotContains(t, logged.String(), "x@y.test")
}

// A delivery error is redacted exactly once, on its way to last_error and to
// the log. Redaction is not idempotent in general: the [address] it inserts
// can close an unclosed "@[" or quote in a second pass and hide the text after
// it. So neither path may redact a string that was already redacted (#358
// review). The raw error here has an address-shaped run that the first pass
// hides and a second pass would widen.
func TestWorker_RedactsTheRawErrorExactlyOnce(t *testing.T) {
	const raw = `550 5.7.1 "Recipient address rejected: a@\"@b`
	const want = `550 5.7.1 "Recipient address rejected: [address]"@b`

	// The retry path writes no reason to the log, so only the stored
	// last_error is asserted here. The give-up path asserts both.
	t.Run("retried", func(t *testing.T) {
		store := newFakeOutbox()
		email := &recorder{fail: errors.New(raw)}
		require.NoError(t, NewOutboxDispatcher(store, []string{"email"}, nil).Dispatch(context.Background(), sampleEvent()))
		w := newTestWorker(store, map[string]notification.Dispatcher{"email": email})

		_, err := w.RunOnce(context.Background())
		require.NoError(t, err)
		require.Equal(t, []string{want}, store.lastErrors)
	})

	t.Run("given up", func(t *testing.T) {
		store := newFakeOutbox()
		email := &recorder{fail: errors.New(raw)}
		require.NoError(t, NewOutboxDispatcher(store, []string{"email"}, nil).Dispatch(context.Background(), sampleEvent()))
		var logged bytes.Buffer
		w := NewWorker(store, map[string]notification.Dispatcher{"email": email}, slog.New(slog.NewJSONHandler(&logged, nil)))
		w.MaxAttempts = 1 // the first claim is the last attempt

		_, err := w.RunOnce(context.Background())
		require.NoError(t, err)
		require.Len(t, store.failed, 1)
		for _, reason := range store.failed {
			require.Equal(t, want, reason, "last_error")
		}
		require.Equal(t, want, loggedGiveUpReason(t, logged.Bytes()), "log line")
	})
}

// loggedGiveUpReason returns the reason field of the give-up log line.
func loggedGiveUpReason(t *testing.T, logged []byte) string {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(logged), []byte("\n")) {
		var rec map[string]any
		require.NoError(t, json.Unmarshal(line, &rec))
		if rec["msg"] == "notification outbox: giving up on a notification" {
			reason, ok := rec["reason"].(string)
			require.True(t, ok, "no reason field on the give-up line")
			return reason
		}
	}
	require.FailNow(t, "no give-up log line was written")
	return ""
}

// A verification event queues for the verification channel and nothing else:
// it has no recipient for email and is never a webhook event. And the
// verification channel queues nothing else (#348).
func TestOutboxDispatcher_VerificationEventsGoOnlyToTheirChannel(t *testing.T) {
	store := newFakeOutbox()
	d := NewOutboxDispatcher(store, []string{"email", "webhook", "verification"}, nil)
	verify := notification.Event{Type: notification.EventRegistrationVerify,
		Payload: map[string]any{"pending_id": uuid.New().String()}}
	require.NoError(t, d.Dispatch(context.Background(), verify))
	require.Equal(t, []string{"verification"}, store.channelsQueued())

	store2 := newFakeOutbox()
	require.NoError(t, NewOutboxDispatcher(store2, []string{"verification"}, nil).Dispatch(context.Background(), sampleEvent()))
	require.Empty(t, store2.channelsQueued(), "a ticket event was queued for the verification channel")
}

func TestVerificationDispatcher(t *testing.T) {
	id := uuid.New()
	var got []uuid.UUID
	d := NewVerificationDispatcher(func(_ context.Context, sent uuid.UUID) error { got = append(got, sent); return nil })

	require.NoError(t, d.Dispatch(context.Background(), sampleEvent()), "a ticket event is ignored")
	require.Empty(t, got)
	require.NoError(t, d.Dispatch(context.Background(), notification.Event{
		Type: notification.EventRegistrationVerify, Payload: map[string]any{"pending_id": id.String()}}))
	require.Equal(t, []uuid.UUID{id}, got)
	require.Error(t, d.Dispatch(context.Background(), notification.Event{Type: notification.EventRegistrationVerify}),
		"a verification event with no id must fail, not vanish")
}
