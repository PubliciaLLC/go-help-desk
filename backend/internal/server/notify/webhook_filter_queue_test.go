package notify

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// The ticket filter (#413) meets the per-hook queue (#448). The filter is
// applied once, where an event is fanned out into one row per hook; after that
// a delivery is its hook's own. These tests pin that seam.

// enqueueSpy remembers every row the webhook dispatcher queues, which the
// fake outbox forgets once a row is delivered and deleted.
type enqueueSpy struct {
	notification.Outbox
	mu   sync.Mutex
	rows [][]byte
}

func (s *enqueueSpy) Enqueue(ctx context.Context, id uuid.UUID, ch string, ev []byte) error {
	s.mu.Lock()
	s.rows = append(s.rows, ev)
	s.mu.Unlock()
	return s.Outbox.Enqueue(ctx, id, ch, ev)
}

// targets lists the hooks that were given a per-hook row.
func (s *enqueueSpy) targets(t *testing.T) []uuid.UUID {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []uuid.UUID
	for _, b := range s.rows {
		ev, err := decodeEvent(b)
		require.NoError(t, err)
		if ev.TargetWebhook != uuid.Nil {
			out = append(out, ev.TargetWebhook)
		}
	}
	return out
}

func spyOn(rig *queueRig) *enqueueSpy {
	spy := &enqueueSpy{Outbox: rig.store}
	rig.disp.queue.Outbox = spy
	return spy
}

func eventWithPriority(p string) notification.Event {
	ev := fixtureReplyEvent(false)
	ev.Attrs = &notification.TicketAttrs{CategoryID: uuid.New(), Priority: p}
	return ev
}

func filtered(h authstore.WebhookConfig, f notification.WebhookFilter) authstore.WebhookConfig {
	h.Filter = f
	return h
}

func setFilter(s *queueStore, id uuid.UUID, f notification.WebhookFilter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.hooks[id]
	h.Filter = f
	s.hooks[id] = h
}

// A hook the filter turns away is not a delivery: no per-hook row, so no
// delivery id, no retry and no history entry. It is checked on a receiver that
// would fail, so a row that did exist would show as retries.
func TestQueueFilter_FilteredOutHookGetsNoDeliveryRow(t *testing.T) {
	match, miss := newReceiver(t, 200), newReceiver(t, 503)
	hm := filtered(hookFor(match.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
	hx := filtered(hookFor(miss.srv.URL), notification.WebhookFilter{Priorities: []string{"critical"}})
	rig := newQueueRig(t, match.srv.Client(), hm, hx)
	spy := spyOn(rig)

	require.NoError(t, rig.front.Dispatch(context.Background(), eventWithPriority("low")))
	rig.drain(t)

	require.Equal(t, []uuid.UUID{hm.ID}, spy.targets(t), "only the matching hook may be given a row")
	require.Equal(t, 0, miss.count(), "the filtered-out hook must not be contacted")
	require.Equal(t, 1, match.count())
	rig.hooks.mu.Lock()
	defer rig.hooks.mu.Unlock()
	require.Len(t, rig.hooks.attempts, 1, "a filtered-out hook has no history entry")
	require.Equal(t, hm.ID, rig.hooks.attempts[0].HookID)
	require.Empty(t, rig.store.failed)
	require.Empty(t, rig.store.retries)
}

// An inverted filter admits the tickets that do not match, and a hook with no
// filter is untouched.
func TestQueueFilter_InvertedAndUnfilteredHooks(t *testing.T) {
	inv, plain, hit := newReceiver(t, 200), newReceiver(t, 200), newReceiver(t, 200)
	hInv := filtered(hookFor(inv.srv.URL), notification.WebhookFilter{Priorities: []string{"critical"}, Invert: true})
	hPlain := hookFor(plain.srv.URL)
	hHit := filtered(hookFor(hit.srv.URL), notification.WebhookFilter{Priorities: []string{"critical"}})
	rig := newQueueRig(t, inv.srv.Client(), hInv, hPlain, hHit)

	require.NoError(t, rig.front.Dispatch(context.Background(), eventWithPriority("critical")))
	rig.drain(t)

	require.Equal(t, 0, inv.count(), "the inverse of critical refuses a critical ticket")
	require.Equal(t, 1, plain.count())
	require.Equal(t, 1, hit.count())
}

// A hook the filter admits is delivered exactly as any other queued hook is:
// a delivery id that survives retries, and the event name.
func TestQueueFilter_MatchingHookGetsTheDeliveryHeaders(t *testing.T) {
	rc := newReceiver(t, 503, 200)
	other := newReceiver(t, 200)
	hm := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
	hx := filtered(hookFor(other.srv.URL), notification.WebhookFilter{Priorities: []string{"high"}})
	rig := newQueueRig(t, rc.srv.Client(), hm, hx)

	require.NoError(t, rig.front.Dispatch(context.Background(), eventWithPriority("low")))
	rig.drain(t)

	require.Equal(t, 2, rc.count())
	id := rc.header(0, "X-GHD-Delivery")
	_, err := uuid.Parse(id)
	require.NoError(t, err, "X-GHD-Delivery must be a uuid")
	require.Equal(t, id, rc.header(1, "X-GHD-Delivery"), "the retry keeps the delivery id")
	require.Equal(t, "ticket.replied", rc.header(0, "X-GHD-Event"))
	require.NotEmpty(t, rc.header(0, SignatureHeader))
	require.Equal(t, id, rig.hooks.last().DeliveryID.String())
	require.Equal(t, 0, other.count())
}

// A hook that failed and is retried is retried alone: the hook that was
// filtered out is not woken up by the retry, nor the healthy one repeated.
func TestQueueFilter_RetryOfAMatchingHookStaysWithThatHook(t *testing.T) {
	flaky := newReceiver(t, 500, 500, 200)
	healthy := newReceiver(t, 200)
	away := newReceiver(t, 200)
	hf := filtered(hookFor(flaky.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
	hh := hookFor(healthy.srv.URL)
	ha := filtered(hookFor(away.srv.URL), notification.WebhookFilter{Priorities: []string{"critical"}})
	rig := newQueueRig(t, flaky.srv.Client(), hf, hh, ha)
	spy := spyOn(rig)

	require.NoError(t, rig.front.Dispatch(context.Background(), eventWithPriority("low")))
	rig.drain(t)

	require.Equal(t, 3, flaky.count())
	require.Equal(t, 1, healthy.count(), "a retry elsewhere must not repeat a healthy hook")
	require.Equal(t, 0, away.count(), "a retry must not reach a hook the filter turned away")
	require.ElementsMatch(t, []uuid.UUID{hf.ID, hh.ID}, spy.targets(t))
}

// Admitted once, it stays admitted: an administrator who edits the filter
// while a delivery is waiting for its next attempt does not cancel it. The
// filter judges the ticket as it was when the event was raised, and that was
// decided at fan-out.
func TestQueueFilter_RetryDoesNotReevaluateAnEditedFilter(t *testing.T) {
	rc := newReceiver(t, 500, 200)
	hook := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
	rig := newQueueRig(t, rc.srv.Client(), hook)

	require.NoError(t, rig.front.Dispatch(context.Background(), eventWithPriority("low")))
	rig.store.makeDue()
	_, err := rig.worker.RunOnce(context.Background()) // the fan-out
	require.NoError(t, err)
	rig.store.makeDue()
	_, err = rig.worker.RunOnce(context.Background()) // the first attempt: 500
	require.NoError(t, err)
	require.Equal(t, 1, rc.count())

	setFilter(rig.hooks, hook.ID, notification.WebhookFilter{Priorities: []string{"critical"}})
	rig.drain(t)

	require.Equal(t, 2, rc.count(), "the retry of an admitted delivery must still be sent")
	require.Equal(t, 200, rig.hooks.last().Result.Status)
}

// Redeliver sends the stored event to that hook whatever the ticket and the
// filter say now. The kept copy has no classification, and an entry exists
// only because the filter admitted the event; a replay is an administrator's
// explicit request.
func TestQueueFilter_RedeliverDoesNotReevaluateTheFilter(t *testing.T) {
	rc := newReceiver(t, 200)
	other := newReceiver(t, 200)
	hook := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
	bystander := hookFor(other.srv.URL)
	rig := newQueueRig(t, rc.srv.Client(), hook, bystander)

	require.NoError(t, rig.front.Dispatch(context.Background(), eventWithPriority("low")))
	rig.drain(t)
	require.Equal(t, 1, rc.count())
	require.Equal(t, 1, other.count())
	var original uuid.UUID
	rig.hooks.mu.Lock()
	for _, a := range rig.hooks.attempts {
		if a.HookID == hook.ID {
			original = a.DeliveryID
		}
	}
	rig.hooks.mu.Unlock()

	// The ticket has since been reclassified, as far as this hook's filter is
	// concerned: neither the old value nor any current one matches it.
	setFilter(rig.hooks, hook.ID, notification.WebhookFilter{Priorities: []string{"critical"}})
	require.NoError(t, rig.disp.Redeliver(context.Background(), hook.ID, original))
	rig.drain(t)

	require.Equal(t, 2, rc.count(), "Redeliver must send even though the filter no longer admits the ticket")
	require.NotEqual(t, rc.header(0, "X-GHD-Delivery"), rc.header(1, "X-GHD-Delivery"))
	require.Equal(t, "ticket.replied", rc.header(1, "X-GHD-Event"))
	require.Equal(t, 1, other.count(), "Redeliver goes to that hook only")
}

// An event queued before #413 carries no attributes. In the queued flow, as in
// the direct one, a filtered hook is then sent to and the fact is logged.
func TestQueueFilter_UnknownAttrsDeliverAndLog(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: []string{"critical"}})
	rig := newQueueRig(t, rc.srv.Client(), hook)
	var buf bytes.Buffer
	rig.disp.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false))) // Attrs nil
	rig.drain(t)

	require.Equal(t, 1, rc.count(), "an extra message beats a missed one")
	require.Contains(t, buf.String(), "not evaluated")
	require.Contains(t, buf.String(), hook.ID.String())
}

// The attributes ride the queue to the fan-out and no further: the per-hook
// row and the kept history copy carry none (they are ids only), so no later
// step can judge the filter on stale values.
func TestQueueFilter_PerHookRowsCarryNoAttrs(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
	rig := newQueueRig(t, rc.srv.Client(), hook)
	spy := spyOn(rig)

	require.NoError(t, rig.front.Dispatch(context.Background(), eventWithPriority("low")))
	rig.drain(t)

	spy.mu.Lock()
	defer spy.mu.Unlock()
	require.Len(t, spy.rows, 1)
	require.NotContains(t, string(spy.rows[0]), "attrs")
	require.Eventually(t, func() bool { return rig.hooks.last().DeliveryID != uuid.Nil }, time.Second, 10*time.Millisecond)
	require.NotContains(t, string(rig.hooks.last().Event), "attrs")
}
