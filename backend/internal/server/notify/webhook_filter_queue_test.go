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
// applied where an event is fanned out into one row per hook, and again at
// every attempt and every Redeliver, against the hook as it is then: an
// administrator who tightens a filter stops what is already queued. These
// tests pin that seam.

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

// The filter is read again at every attempt, as the hook's events and URL
// are: an administrator who edits it while a delivery waits for its next try
// changes what that try does. The row carries the ticket's attributes as they
// were when the event was raised, and they are judged against the hook as it
// is now. Tightened (or inverted) past the ticket, the retry is dropped;
// widened, it still goes out.
func TestQueueFilter_RetryRechecksTheCurrentFilter(t *testing.T) {
	low := []string{"low"}
	cases := []struct {
		name   string
		format string
		after  notification.WebhookFilter
		sent   bool
	}{
		{"tightened past the ticket", "raw", notification.WebhookFilter{Priorities: []string{"critical"}}, false},
		{"tightened past the ticket, chat row", "slack", notification.WebhookFilter{Priorities: []string{"critical"}}, false},
		{"inverted", "raw", notification.WebhookFilter{Priorities: low, Invert: true}, false},
		{"widened", "raw", notification.WebhookFilter{Priorities: []string{"low", "critical"}}, true},
		{"widened, chat row", "slack", notification.WebhookFilter{Priorities: []string{"low", "critical"}}, true},
		{"removed", "raw", notification.WebhookFilter{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := newReceiver(t, 500, 200)
			hook := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: low})
			hook.PayloadFormat = tc.format
			rig := newQueueRig(t, rc.srv.Client(), hook)
			var buf bytes.Buffer
			rig.disp.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			require.NoError(t, rig.front.Dispatch(context.Background(), eventWithPriority("low")))
			rig.store.makeDue()
			_, err := rig.worker.RunOnce(context.Background()) // the fan-out
			require.NoError(t, err)
			rig.store.makeDue()
			_, err = rig.worker.RunOnce(context.Background()) // the first attempt: 500
			require.NoError(t, err)
			require.Equal(t, 1, rc.count())

			setFilter(rig.hooks, hook.ID, tc.after)
			rig.drain(t)

			if tc.sent {
				require.Equal(t, 2, rc.count(), "the retry must still be sent")
				require.Equal(t, 200, rig.hooks.last().Result.Status)
				return
			}
			require.Equal(t, 1, rc.count(), "the retry must not be sent: the filter no longer admits the ticket")
			require.Empty(t, rig.store.rows, "the dropped row is settled, not left to retry")
			require.Empty(t, rig.store.failed, "settled, not failed")
			rig.hooks.mu.Lock()
			defer rig.hooks.mu.Unlock()
			require.Len(t, rig.hooks.attempts, 1, "a dropped retry adds no history entry")
			require.Contains(t, buf.String(), "filter")
		})
	}
}

// A row queued before #413 has no attributes to judge, so a filter added since
// cannot turn it away: it is sent, and the fact is logged. The same fail-open
// rule as at the fan-out.
func TestQueueFilter_RowWithoutAttrsIsDeliveredWhateverTheFilterSaysNow(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := hookFor(rc.srv.URL) // no filter when the row is made
	rig := newQueueRig(t, rc.srv.Client(), hook)
	var buf bytes.Buffer
	rig.disp.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false))) // Attrs nil
	rig.store.makeDue()
	_, err := rig.worker.RunOnce(context.Background()) // the fan-out only
	require.NoError(t, err)
	require.Equal(t, 0, rc.count())

	setFilter(rig.hooks, hook.ID, notification.WebhookFilter{Priorities: []string{"critical"}})
	rig.drain(t)

	require.Equal(t, 1, rc.count(), "an extra message beats a missed one")
	require.Contains(t, buf.String(), "not evaluated")
	require.Contains(t, buf.String(), hook.ID.String())
}

func firstDelivery(t *testing.T, rig *queueRig, hookID uuid.UUID) uuid.UUID {
	t.Helper()
	rig.hooks.mu.Lock()
	defer rig.hooks.mu.Unlock()
	for _, a := range rig.hooks.attempts {
		if a.HookID == hookID {
			return a.DeliveryID
		}
	}
	t.Fatal("the hook has no delivery")
	return uuid.Nil
}

// Redeliver judges the filter too, against the ticket as it is now: the kept
// copy has no attributes by design, so they are read from the ticket, the way
// a chat message's content is. A ticket that no longer matches is refused with
// a reason and nothing is queued, as for a hook that dropped the event.
func TestQueueFilter_RedeliverRechecksTheFilterAgainstTheTicketNow(t *testing.T) {
	cases := []struct {
		name     string
		format   string
		priority string // the ticket now
		excluded bool
	}{
		{"raw, ticket no longer matches", "raw", "critical", true},
		{"raw, ticket still matches", "raw", "low", false},
		{"slack, ticket no longer matches", "slack", "critical", true},
		{"slack, ticket still matches", "slack", "low", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := newReceiver(t, 200)
			hook := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
			hook.PayloadFormat = tc.format
			rig := newQueueRig(t, rc.srv.Client(), hook)
			src := newFakeSource()
			rig.disp.queue.Source = src
			ev := eventWithPriority("low")
			require.NoError(t, rig.front.Dispatch(context.Background(), ev))
			rig.drain(t)
			require.Equal(t, 1, rc.count())
			original := firstDelivery(t, rig, hook.ID)

			now := &notification.TicketAttrs{CategoryID: ev.Attrs.CategoryID, Priority: tc.priority}
			src.facts[ev.TicketID] = TicketFacts{Subject: "now", Attrs: now}
			before := len(rig.store.order)
			err := rig.disp.Redeliver(context.Background(), hook.ID, original)
			rig.drain(t)

			if tc.excluded {
				require.ErrorIs(t, err, ErrWebhookFilterExcludes)
				require.Equal(t, before, len(rig.store.order), "nothing is queued")
				require.Equal(t, 1, rc.count())
				return
			}
			require.NoError(t, err)
			require.Equal(t, 2, rc.count())
			require.NotEqual(t, rc.header(0, "X-GHD-Delivery"), rc.header(1, "X-GHD-Delivery"))
			require.Equal(t, "ticket.replied", rc.header(1, "X-GHD-Event"))
		})
	}
}

// A ticket that is found but carries no attributes (a source that cannot say)
// leaves the filter nothing to judge. Like a queued row without attributes, the
// replay is sent and the fact logged. The production source always fills them,
// so this is the same fail-open as deliverQueued's, kept for that seam only.
func TestQueueFilter_RedeliverWithoutTicketAttrsFailsOpen(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
	rig := newQueueRig(t, rc.srv.Client(), hook)
	require.NoError(t, rig.front.Dispatch(context.Background(), eventWithPriority("low")))
	rig.drain(t)
	original := firstDelivery(t, rig, hook.ID)
	setFilter(rig.hooks, hook.ID, notification.WebhookFilter{Priorities: []string{"critical"}})
	src := newFakeSource()
	src.facts[fixtureTicketID] = TicketFacts{Subject: "now"}
	rig.disp.queue.Source = src
	var buf bytes.Buffer
	rig.disp.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	require.NoError(t, rig.disp.Redeliver(context.Background(), hook.ID, original))
	rig.drain(t)

	require.Equal(t, 2, rc.count(), "an extra message beats a missed one")
	require.Contains(t, buf.String(), "not evaluated")
}

// A hook with a filter cannot be checked against a ticket that cannot be read
// (#499): the ticket is gone, or no source is wired. The replay is refused with
// a reason and nothing is queued, whatever the payload format. An inverted
// filter is a filter too. A hook with no filter has nothing to check and is
// replayed as before.
func TestQueueFilter_RedeliverWithTicketGone(t *testing.T) {
	low := notification.WebhookFilter{Priorities: []string{"low"}}
	for _, tc := range []struct {
		name       string
		format     string
		filter     notification.WebhookFilter
		noSource   bool
		unverified bool
	}{
		{"raw, filter", "raw", low, false, true},
		{"slack, filter", "slack", low, false, true},
		{"raw, inverted filter", "raw", notification.WebhookFilter{Priorities: []string{"critical"}, Invert: true}, false, true},
		{"raw, category-only filter", "raw", notification.WebhookFilter{CategoryIDs: []uuid.UUID{uuid.New()}}, false, true},
		{"raw, filter, no source wired", "raw", low, true, true},
		{"slack, filter, no source wired", "slack", low, true, true},
		{"raw, no filter", "raw", notification.WebhookFilter{}, false, false},
		{"slack, no filter", "slack", notification.WebhookFilter{}, false, false},
		{"raw, no filter, no source wired", "raw", notification.WebhookFilter{}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := newReceiver(t, 200)
			hook := hookFor(rc.srv.URL)
			hook.PayloadFormat = tc.format
			rig := newQueueRig(t, rc.srv.Client(), hook)
			require.NoError(t, rig.front.Dispatch(context.Background(), eventWithPriority("low")))
			rig.drain(t)
			require.Equal(t, 1, rc.count())
			original := firstDelivery(t, rig, hook.ID)
			setFilter(rig.hooks, hook.ID, tc.filter)
			if !tc.noSource {
				rig.disp.queue.Source = newFakeSource() // knows no ticket: it is gone
			} else {
				rig.disp.queue.Source = nil
			}
			before := len(rig.store.order)

			err := rig.disp.Redeliver(context.Background(), hook.ID, original)
			rig.drain(t)

			if tc.unverified {
				require.ErrorIs(t, err, ErrWebhookFilterUnverifiable)
				require.NotErrorIs(t, err, ErrWebhookFilterExcludes)
				require.Equal(t, before, len(rig.store.order), "nothing is queued")
				require.Equal(t, 1, rc.count())
				return
			}
			require.NoError(t, err)
			require.Equal(t, 2, rc.count(), "a hook with no filter is still replayed")
		})
	}
}

// The replay's queued row carries the ticket's attributes too, so the same
// check can stop it at its own send if the filter is tightened in between.
func TestQueueFilter_RedeliveredRowIsRecheckedAtSend(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
	rig := newQueueRig(t, rc.srv.Client(), hook)
	src := newFakeSource()
	rig.disp.queue.Source = src
	ev := eventWithPriority("low")
	require.NoError(t, rig.front.Dispatch(context.Background(), ev))
	rig.drain(t)
	original := firstDelivery(t, rig, hook.ID)
	now := &notification.TicketAttrs{CategoryID: ev.Attrs.CategoryID, Priority: "low"}
	src.facts[ev.TicketID] = TicketFacts{Attrs: now}
	spy := spyOn(rig)

	require.NoError(t, rig.disp.Redeliver(context.Background(), hook.ID, original))
	spy.mu.Lock()
	require.Len(t, spy.rows, 1)
	queued, err := decodeEvent(spy.rows[0])
	spy.mu.Unlock()
	require.NoError(t, err)
	require.Equal(t, now, queued.Attrs, "the replay's row holds the ticket's attributes")

	setFilter(rig.hooks, hook.ID, notification.WebhookFilter{Priorities: []string{"critical"}})
	rig.drain(t)
	require.Equal(t, 1, rc.count(), "tightened after the replay was accepted: dropped at its attempt")
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

// The attributes stay in every queued per-hook row, whatever the hook's
// payload format, because a retry judges the filter again and needs them. They
// are internal: ids and a priority, never sent. Neither the bytes the receiver
// gets nor the copy kept for Redeliver may hold them, whichever renderer built
// the body.
func TestQueueFilter_PerHookRowsKeepAttrsInternally(t *testing.T) {
	for _, format := range []string{"raw", "", "slack", "teams", "discord", "jira"} {
		t.Run("format="+format, func(t *testing.T) {
			rc := newReceiver(t, 500, 200)
			bodies := capture(rc)
			hook := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
			hook.PayloadFormat = format
			rig := newQueueRig(t, rc.srv.Client(), hook)
			spy := spyOn(rig)
			ev := eventWithPriority("low")
			group := uuid.New()
			ev.Attrs.AssigneeGroupID = &group

			require.NoError(t, rig.front.Dispatch(context.Background(), ev))
			rig.drain(t)

			spy.mu.Lock()
			defer spy.mu.Unlock()
			require.Len(t, spy.rows, 1)
			got, err := decodeEvent(spy.rows[0])
			require.NoError(t, err)
			require.Equal(t, ev.Attrs, got.Attrs, "the queued row keeps the attributes")

			sent := bodies()
			require.Len(t, sent, 2, "the first attempt failed and was retried")
			for _, b := range sent {
				for _, leak := range []string{"attrs", "category_id", "assignee_group_id",
					ev.Attrs.CategoryID.String(), group.String()} {
					require.NotContains(t, b, leak, "a sent body must not carry the filter attributes")
				}
			}
			require.Eventually(t, func() bool { return rig.hooks.last().DeliveryID != uuid.Nil }, time.Second, 10*time.Millisecond)
			kept := string(rig.hooks.last().Event)
			for _, leak := range []string{"attrs", ev.Attrs.CategoryID.String(), group.String()} {
				require.NotContains(t, kept, leak, "the history copy keeps no attributes")
			}
		})
	}
}

// The row kept for 30 days after a delivery finally fails is scrubbed of its
// content and holds no attributes either: they are for the next attempt, and
// there is none.
func TestQueueFilter_FailedRowScrubKeepsNoAttrs(t *testing.T) {
	for _, format := range []string{"raw", "slack"} {
		t.Run("format="+format, func(t *testing.T) {
			rc := newReceiver(t, 500)
			hook := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
			hook.PayloadFormat = format
			rig := newQueueRig(t, rc.srv.Client(), hook)
			ev := eventWithPriority("low")

			require.NoError(t, rig.front.Dispatch(context.Background(), ev))
			rig.drain(t)

			require.Len(t, rig.store.failed, 1)
			for id := range rig.store.failed {
				kept := string(rig.store.rows[id].event)
				require.NotContains(t, kept, "attrs")
				require.NotContains(t, kept, ev.Attrs.CategoryID.String())
				require.Contains(t, kept, ev.TicketID.String(), "ids stay")
			}
		})
	}
}
