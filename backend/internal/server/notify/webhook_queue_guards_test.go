package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// contentStrings are the values contentEvent carries outside ids. None may
// reach a raw body or the copy kept for Redeliver.
var contentStrings = []string{
	"guest@example.com", "reporter@example.com", "customer@example.com",
	"SECRET-REPLY-TEXT", "SECRET-SUBJECT", "TRK-SECRET-7", "SECRET-PRIORITY", "SECRET-STATUS",
}

// contentReplyID is the reply contentEvent names. It is an id, but not one the
// raw body carries.
var contentReplyID = uuid.New()

// contentEvent is a public reply carrying every kind of content the domain
// can put on an event, in the typed fields and in Payload.
func contentEvent() notification.Event {
	linked := uuid.New()
	return notification.Event{
		Type:     notification.EventTicketReplied,
		TicketID: uuid.New(),
		Payload: map[string]any{
			"guest_email":    "guest@example.com",
			"reporter_email": "reporter@example.com",
			"ReplyBody":      "SECRET-REPLY-TEXT",
			"Subject":        "SECRET-SUBJECT",
			"TrackingNumber": "TRK-SECRET-7",
			"Priority":       "SECRET-PRIORITY",
			"internal":       false,
			"reply_id":       contentReplyID,
			"target_id":      linked,
			"link_type":      "related",
			"new_status_id":  uuid.New(),
		},
		OccurredAt:     time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC),
		TrackingNumber: "TRK-SECRET-7",
		Recipient:      "customer@example.com",
		GuestLink:      true,
		Subject:        "SECRET-SUBJECT",
		StatusName:     "SECRET-STATUS",
	}
}

// setHook edits a stored hook, as an administrator would between attempts.
func (r *queueRig) setHook(id uuid.UUID, edit func(*authstore.WebhookConfig)) {
	r.hooks.mu.Lock()
	defer r.hooks.mu.Unlock()
	h := r.hooks.hooks[id]
	edit(&h)
	r.hooks.hooks[id] = h
}

// capture records every body a receiver is sent.
func capture(rc *receiver) func() []string {
	var mu sync.Mutex
	var bodies []string
	rc.onRequest(func(_ http.ResponseWriter, r *http.Request) bool {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		return false
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

// ── Raw is ids only (#448, Erik's decision) ──────────────────────────────────

func TestRawBody_CarriesOnlyIdsAndFlags(t *testing.T) {
	ev := contentEvent()
	body, err := rawBody(ev)
	require.NoError(t, err)

	for _, leak := range contentStrings {
		require.NotContains(t, string(body), leak)
	}
	for _, key := range []string{"guest_email", "reporter_email", "ReplyBody", "Subject", "TrackingNumber", "Priority"} {
		require.NotContains(t, string(body), key)
	}
	for _, want := range []string{`"Type":"ticket.replied"`, ev.TicketID.String(), "target_id", "new_status_id", "OccurredAt"} {
		require.Contains(t, string(body), want)
	}
	// Ids and the event name only: not the link type, not the internal flag (the
	// event name already says note or reply), not the reply id.
	for _, gone := range []string{"link_type", "internal", "reply_id", contentReplyID.String()} {
		require.NotContains(t, string(body), gone)
	}
}

// An unknown Payload key is left out, so a content key added to an event
// tomorrow does not reach a receiver until someone decides it should.
func TestRawBody_IsAnAllowList(t *testing.T) {
	ev := notification.Event{Type: notification.EventTicketCreated, TicketID: uuid.New(),
		Payload: map[string]any{"some_new_key": "SECRET-NEW"}}
	body, err := rawBody(ev)
	require.NoError(t, err)
	require.NotContains(t, string(body), "SECRET-NEW")
}

func TestRawBody_DoesNotChangeTheEventItWasGiven(t *testing.T) {
	ev := contentEvent()
	_, err := rawBody(ev)
	require.NoError(t, err)
	require.Equal(t, "SECRET-REPLY-TEXT", ev.Payload["ReplyBody"], "chat formats render from the same event")
}

func TestQueue_RawHooksReceiveIdsOnly_ChatHooksKeepTheirMessage(t *testing.T) {
	rc := newReceiver(t, 200)
	bodies := capture(rc)
	raw := hookFor(rc.srv.URL + "/raw")
	slack := hookFor(rc.srv.URL + "/slack")
	slack.PayloadFormat = "slack"
	rig := newQueueRig(t, rc.srv.Client(), raw, slack)

	require.NoError(t, rig.front.Dispatch(context.Background(), contentEvent()))
	rig.drain(t)

	got := bodies()
	require.Len(t, got, 2)
	var sawRaw, sawSlack bool
	for _, b := range got {
		if strings.Contains(b, `"Type"`) {
			sawRaw = true
			for _, leak := range contentStrings {
				require.NotContains(t, b, leak, "raw body")
			}
			continue
		}
		sawSlack = true
		// What the Slack message says is unchanged: the subject and the reply.
		require.Contains(t, b, "SECRET-SUBJECT")
		require.Contains(t, b, "SECRET-REPLY-TEXT")
		require.Contains(t, b, "TRK-SECRET-7")
		require.NotContains(t, b, "customer@example.com")
		require.NotContains(t, b, "guest@example.com")
	}
	require.True(t, sawRaw && sawSlack)
}

func TestDispatch_WithoutAQueueAlsoSendsIdsOnly(t *testing.T) {
	rc := newReceiver(t, 200)
	bodies := capture(rc)
	disp := NewWebhookDispatcher(fakeWebhookStore{hookFor(rc.srv.URL)}, fixtureBaseURL, quietLog())
	disp.client = rc.srv.Client()

	require.NoError(t, disp.Dispatch(context.Background(), contentEvent()))
	require.Eventually(t, func() bool { return len(bodies()) == 1 }, 2*time.Second, 10*time.Millisecond)
	for _, leak := range contentStrings {
		require.NotContains(t, bodies()[0], leak)
	}
}

// The outbox row of a raw hook is the event as it will be sent, plus nothing
// that identifies a person; a chat hook's row keeps the text its message needs
// but still no address.
func TestFanOut_RowsHoldNoAddressAndRawRowsNoContent(t *testing.T) {
	rc := newReceiver(t, 200)
	raw := hookFor(rc.srv.URL + "/raw")
	slack := hookFor(rc.srv.URL + "/slack")
	slack.PayloadFormat = "slack"
	rig := newQueueRig(t, rc.srv.Client(), raw, slack)

	require.NoError(t, rig.front.Dispatch(context.Background(), contentEvent()))
	_, err := rig.worker.RunOnce(context.Background()) // the fan-out only
	require.NoError(t, err)

	rig.store.mu.Lock()
	defer rig.store.mu.Unlock()
	var rows int
	for _, id := range rig.store.order {
		r := rig.store.rows[id]
		if r == nil || r.channel != "webhook" || !strings.Contains(string(r.event), "webhook_id") {
			continue
		}
		rows++
		s := string(r.event)
		require.NotContains(t, s, "@example.com", "no address in any queued row")
		if strings.Contains(s, raw.ID.String()) {
			for _, leak := range contentStrings {
				require.NotContains(t, s, leak, "raw hook row")
			}
		} else {
			require.Contains(t, s, "SECRET-REPLY-TEXT", "the chat message needs it")
		}
	}
	require.Equal(t, 2, rows)
}

// Redeliver of a chat hook works from ids only, so its message has the
// headline and no subject or text. Said in DESIGN; pinned here.
func TestRedeliver_ChatHookGetsAMessageWithoutContent(t *testing.T) {
	rc := newReceiver(t, 200)
	bodies := capture(rc)
	slack := hookFor(rc.srv.URL)
	slack.PayloadFormat = "slack"
	rig := newQueueRig(t, rc.srv.Client(), slack)
	require.NoError(t, rig.front.Dispatch(context.Background(), contentEvent()))
	rig.drain(t)

	require.NoError(t, rig.disp.Redeliver(context.Background(), slack.ID, rig.hooks.last().DeliveryID))
	rig.drain(t)

	got := bodies()
	require.Len(t, got, 2)
	require.Contains(t, got[0], "SECRET-REPLY-TEXT")
	require.Contains(t, got[1], "New reply")
	require.NotContains(t, got[1], "SECRET-REPLY-TEXT")
	require.NotContains(t, got[1], "SECRET-SUBJECT")
}

// ── The hook may change while a delivery waits ───────────────────────────────

func TestQueue_UnsubscribedBetweenQueueingAndSendingIsSkipped(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := hookFor(rc.srv.URL)
	rig := newQueueRig(t, rc.srv.Client(), hook)

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	_, err := rig.worker.RunOnce(context.Background()) // fan-out: the hook is subscribed now
	require.NoError(t, err)

	rig.setHook(hook.ID, func(h *authstore.WebhookConfig) { h.Events = []string{"ticket.closed"} })
	rig.drain(t)

	require.Equal(t, 0, rc.count(), "an administrator removed the event; nothing more is sent for it")
	require.Empty(t, rig.store.failed, "settled, not failed")
	require.Empty(t, rig.hooks.attempts)
}

func TestQueue_UnsubscribedHookStopsItsRetries(t *testing.T) {
	rc := newReceiver(t, 500, 200)
	hook := hookFor(rc.srv.URL)
	rig := newQueueRig(t, rc.srv.Client(), hook)

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.store.makeDue()
	for range 2 { // fan-out, then the first attempt, which fails
		_, err := rig.worker.RunOnce(context.Background())
		require.NoError(t, err)
	}
	require.Equal(t, 1, rc.count())

	rig.setHook(hook.ID, func(h *authstore.WebhookConfig) { h.Events = []string{"ticket.closed"} })
	rig.drain(t)

	require.Equal(t, 1, rc.count(), "the retry must not go out")
}

func TestRedeliver_RefusesAnEventTheHookNoLongerSubscribesTo(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := hookFor(rc.srv.URL)
	rig := newQueueRig(t, rc.srv.Client(), hook)
	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.drain(t)
	delivery := rig.hooks.last().DeliveryID

	rig.setHook(hook.ID, func(h *authstore.WebhookConfig) { h.Events = []string{"ticket.closed"} })
	before := len(rig.store.order)

	require.ErrorIs(t, rig.disp.Redeliver(context.Background(), hook.ID, delivery), ErrWebhookNotSubscribed)
	require.Equal(t, before, len(rig.store.order), "nothing is queued")
	require.Equal(t, 1, rc.count())
}

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// A retry queued for the old address is dropped when the URL changes; it must
// not be sent to the new one.
func TestQueue_URLChangedBetweenAttemptsDropsTheRetry(t *testing.T) {
	oldRC := newReceiver(t, 500)
	newRC := newReceiver(t, 200)
	hook := hookFor(oldRC.srv.URL + "/old-token")
	rig := newQueueRig(t, oldRC.srv.Client(), hook)

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.store.makeDue()
	for range 2 {
		_, err := rig.worker.RunOnce(context.Background())
		require.NoError(t, err)
	}
	require.Equal(t, 1, oldRC.count())

	rig.setHook(hook.ID, func(h *authstore.WebhookConfig) { h.URL = newRC.srv.URL + "/new" })
	rig.drain(t)

	require.Equal(t, 0, newRC.count(), "the old event must not go to the new address")
	require.Equal(t, 1, oldRC.count())
	require.Empty(t, rig.store.failed)
}

// The outbox holds a fingerprint of the address, never the address: a URL can
// carry a token.
func TestFanOut_RowsHoldAHashOfTheURLNotTheURL(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := hookFor(rc.srv.URL + "/token-abc")
	rig := newQueueRig(t, rc.srv.Client(), hook)
	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	_, err := rig.worker.RunOnce(context.Background())
	require.NoError(t, err)

	rig.store.mu.Lock()
	defer rig.store.mu.Unlock()
	var found bool
	for _, id := range rig.store.order {
		r := rig.store.rows[id]
		if r == nil || !strings.Contains(string(r.event), "webhook_id") {
			continue
		}
		found = true
		require.NotContains(t, string(r.event), "token-abc")
		require.NotContains(t, string(r.event), rc.srv.URL)
		require.Contains(t, string(r.event), sha(hook.URL))
	}
	require.True(t, found)
}

// The secret is not part of the fingerprint: a receiver that rotated its key
// gets the retry signed with the new one.
func TestQueue_SecretChangedBetweenAttemptsUsesTheCurrentSecret(t *testing.T) {
	rc := newReceiver(t, 500, 200)
	bodies := capture(rc)
	hook := hookFor(rc.srv.URL)
	rig := newQueueRig(t, rc.srv.Client(), hook)

	require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
	rig.store.makeDue()
	for range 2 {
		_, err := rig.worker.RunOnce(context.Background())
		require.NoError(t, err)
	}
	rig.setHook(hook.ID, func(h *authstore.WebhookConfig) { h.Secret = "rotated" })
	rig.drain(t)

	require.Equal(t, 2, rc.count())
	body := bodies()[1]
	require.Equal(t, "sha256="+hmacSHA256("rotated", []byte(body)), rc.header(1, SignatureHeader))
}

// ── Retry-After ──────────────────────────────────────────────────────────────

// A number of seconds too large for a time.Duration must not wrap round to
// something small or negative.
func TestRetryAfter_HugeValuesAreCappedNotWrapped(t *testing.T) {
	now := time.Now()
	for _, v := range []string{"9223372036854775807", "9223372037", "99999999999999", "3601"} {
		got := parseRetryAfter(v, now)
		require.Equal(t, maxRetryAfter, got, "Retry-After %q", v)
	}
	require.Equal(t, 3600*time.Second, parseRetryAfter("3600", now))
	// A date far ahead is capped the same way.
	require.Equal(t, maxRetryAfter, parseRetryAfter(now.Add(1000*time.Hour).UTC().Format(http.TimeFormat), now))
}
