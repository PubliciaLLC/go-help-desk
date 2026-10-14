package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/publiciallc/go-help-desk/backend/internal/safehttp"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// WebhookEvents is the list of event types that can be subscribed to via webhooks.
// guest.link_resent is excluded because webhooks are defined as HTTP callbacks
// for ticket lifecycle events; guest link resends are not ticket changes.
var WebhookEvents = []notification.EventType{
	notification.EventTicketCreated,
	notification.EventTicketAssigned,
	notification.EventTicketStatusChanged,
	notification.EventTicketReplied,
	notification.EventTicketNoteAdded,
	notification.EventTicketResolved,
	notification.EventTicketClosed,
	notification.EventTicketReopened,
	notification.EventTicketLinked,
}

// IsWebhookEvent reports whether e is "*" or one of WebhookEvents.
// Exact, case-sensitive, untrimmed: the same comparison hookSubscribes makes.
func IsWebhookEvent(e string) bool {
	if e == "*" {
		return true
	}
	for _, event := range WebhookEvents {
		if e == string(event) {
			return true
		}
	}
	return false
}

// Delivery error classes stored on the hook. A class, never a message: see
// migration 000034, whose CHECK lists exactly these.
const (
	DeliveryHTTPStatus     = "http_status" // a response came back, not 2xx
	DeliveryTimeout        = "timeout"
	DeliveryDNS            = "dns"
	DeliveryTLS            = "tls"
	DeliveryBlockedAddress = "blocked_address" // safehttp refused an internal address
	DeliveryConnection     = "connection"      // refused, reset, closed without a response
	DeliveryOther          = "other"
)

// DeliveryErrors lists every class, for the test that pins it to the CHECK.
var DeliveryErrors = []string{DeliveryHTTPStatus, DeliveryTimeout, DeliveryDNS, DeliveryTLS, DeliveryBlockedAddress, DeliveryConnection, DeliveryOther}

// WebhookStore is the interface needed to load enabled webhook configs and record delivery results.
type WebhookStore interface {
	ListEnabledWebhooks(ctx context.Context) ([]authstore.WebhookConfig, error)
	RecordWebhookDelivery(ctx context.Context, id uuid.UUID, url string, d authstore.WebhookDelivery) error
}

// WebhookLookup reads one hook, for the delivery of a queued row.
type WebhookLookup interface {
	GetWebhook(ctx context.Context, id uuid.UUID) (authstore.WebhookConfig, error)
}

// WebhookHistory keeps each delivery's attempts and the event Redeliver sends
// again (#448).
type WebhookHistory interface {
	RecordWebhookAttempt(ctx context.Context, a authstore.WebhookAttempt) error
	GetWebhookDeliveryEvent(ctx context.Context, hookID, deliveryID uuid.UUID) ([]byte, error)
}

// TicketFacts are the values a chat or ITSM message names, read from the ticket
// as it is now.
type TicketFacts struct {
	Subject        string
	TrackingNumber string
	StatusName     string // the ticket's current status
	Priority       string
}

// EventSource reads the ticket fresh, so Redeliver can rebuild a chat message
// without the history having kept any content.
type EventSource interface {
	// TicketFacts reports false when the ticket no longer exists.
	TicketFacts(ctx context.Context, ticketID uuid.UUID) (TicketFacts, bool, error)
	// PublicReplyBody returns the text of a reply that exists on that ticket and
	// is public. It reports false for a missing, deleted or internal reply, and
	// never returns the text of a note.
	PublicReplyBody(ctx context.Context, ticketID, replyID uuid.UUID) (string, bool, error)
}

// EventRewriter is implemented by an outbox that can replace the event of a
// queued row. The webhook dispatcher uses it to clear a failed row's content.
type EventRewriter interface {
	RewriteEvent(ctx context.Context, id uuid.UUID, event []byte) error
}

// WebhookQueue is what turns a webhook send from fire-and-forget into
// per-hook, retried deliveries (#448). Without one a dispatcher sends at once
// and never retries.
type WebhookQueue struct {
	Outbox  notification.Outbox
	Hooks   WebhookLookup
	History WebhookHistory
	Wake    func() // asks the worker to look now; may be nil
	// Source lets Redeliver rebuild a chat or ITSM message from the ticket as it
	// is now. May be nil, in which case such a replay has its headline only.
	Source EventSource
}

// Redeliver refusals.
var (
	ErrWebhookNotFound  = errors.New("webhook not found")
	ErrWebhookDisabled  = errors.New("webhook is disabled")
	ErrDeliveryNotFound = errors.New("delivery not found")

	// ErrWebhookNotSubscribed: the hook no longer lists the delivery's event.
	ErrWebhookNotSubscribed = errors.New("webhook is not subscribed to this event")
)

// WebhookDispatcher sends HTTP POST payloads to configured webhook URLs.
type WebhookDispatcher struct {
	store   WebhookStore
	client  *http.Client
	baseURL string // used only to build the staff ticket link chat/ITSM formats carry
	log     *slog.Logger
	queue   *WebhookQueue
}

// SetSource sets the queue's EventSource after construction, for wiring where
// the source (the ticket service) needs the dispatcher to exist first. The
// dispatcher must have a queue.
func (d *WebhookDispatcher) SetSource(src EventSource) {
	d.queue.Source = src
}

// WithQueue makes the dispatcher queue one retried delivery per subscribed
// hook instead of sending at once. The webhook outbox channel then does two
// jobs, told apart by Event.TargetWebhook: an event with none is fanned out
// into one row per hook; an event with one is delivered to that hook.
func (d *WebhookDispatcher) WithQueue(q WebhookQueue) *WebhookDispatcher {
	d.queue = &q
	return d
}

// NewWebhookDispatcher returns a WebhookDispatcher with sensible timeouts.
// baseURL is the same value the email dispatcher uses for ticket links
// (cfg.BaseURL); it is never used to reach the hook target itself.
// log is used to report delivery failures and other operational issues.
func NewWebhookDispatcher(store WebhookStore, baseURL string, log *slog.Logger) *WebhookDispatcher {
	if log == nil {
		// send runs unrecovered in its own goroutine; a nil logger would
		// panic there on the first delivery failure and take the process
		// down with it. Defaulting here removes that trap for any caller
		// that builds a dispatcher without one.
		log = slog.Default()
	}
	return &WebhookDispatcher{
		store:   store,
		baseURL: baseURL,
		log:     log,
		// Guarded: the URL is operator-supplied and the app container can
		// reach the database, the antivirus daemon and cloud metadata, none
		// of which are reachable from outside.
		client: safehttp.Client(10 * time.Second),
	}
}

// Dispatch sends the event as JSON to every enabled webhook that subscribes
// to this event type. Failures are logged but do not propagate.
func (d *WebhookDispatcher) Dispatch(ctx context.Context, event notification.Event) error {
	// guest.link_resent (and any other event type not in WebhookEvents) is
	// deliberately excluded from webhooks — IsWebhookEvent already rejects it
	// on create/update — but hookSubscribes has no event-type filter of its
	// own, so a "*" (or legacy empty-events) subscription would otherwise
	// receive it anyway, contradicting that exclusion. Filtered here, before
	// any hook is considered, rather than inside hookSubscribes: it is a
	// property of the EVENT, not of any one hook's subscription shape. See
	// #212.
	if !IsWebhookEvent(string(event.Type)) {
		return nil
	}
	if d.queue != nil {
		if event.TargetWebhook != uuid.Nil {
			return d.deliverQueued(ctx, event)
		}
		return d.fanOut(ctx, event)
	}

	hooks, err := d.store.ListEnabledWebhooks(ctx)
	if err != nil {
		// A DB error listing hooks means zero deliveries to every webhook;
		// previously this was indistinguishable from "nothing subscribed".
		// See #213.
		d.log.ErrorContext(ctx, "webhook dispatch skipped: could not list enabled webhooks",
			"event", event.Type, "error", err)
		return nil // store failure is non-fatal
	}

	// Marshalled once: the raw wire body every "raw" (and empty-format)
	// subscription receives, ids only (rawBody). bodyFor below reshapes the
	// full event per format for the others; it never touches this slice for
	// those hooks.
	payload, err := rawBody(event)
	if err != nil {
		d.log.ErrorContext(ctx, "webhook dispatch skipped: event could not be marshalled",
			"event", event.Type, "error", err)
		return nil
	}

	for _, hook := range hooks {
		if !hookSubscribes(hook, event.Type) {
			continue
		}
		// The ticket filter (#413) decides whether this hook fires for this
		// event, and nothing else: the body below is built the same way for
		// every hook. A filtered-out event is not a delivery, so nothing is
		// recorded for the hook.
		if !d.admits(ctx, hook, event) {
			continue
		}
		body, err := bodyFor(hook, event, payload, d.baseURL)
		if err != nil {
			// Unknown payload_format: skip this hook rather than falling
			// back to raw. A Slack URL fed the full raw event is a
			// delivery bug, not a degraded-but-working delivery.
			d.log.WarnContext(ctx, "webhook skipped: payload could not be built",
				"webhook_id", hook.ID, "payload_format", hook.PayloadFormat,
				"event", event.Type, "error", err)
			continue
		}
		// Fire-and-forget per webhook, as before; the result is recorded on the
		// hook so a failing one shows on the admin page (#157), not only in the log.
		go func(hook authstore.WebhookConfig, body []byte) {
			res, _ := d.post(context.Background(), hook, body, uuid.New(), event.Type)
			d.record(hook, res)
		}(hook, body)
	}
	return nil
}

// admits reports whether the hook's ticket filter lets the event through. An
// event with no attrs (queued before #413) is let through and logged: an extra
// message beats a missed one.
func (d *WebhookDispatcher) admits(ctx context.Context, hook authstore.WebhookConfig, ev notification.Event) bool {
	admit, known := hook.Filter.Admits(ev.Attrs)
	if !known {
		d.log.WarnContext(ctx, "webhook filter not evaluated: event carries no ticket attributes; delivering",
			"webhook_id", hook.ID, "event", ev.Type, "ticket_id", ev.TicketID)
	}
	return admit
}

// fanOut turns the webhook channel's one row for an event into one row per
// subscribed hook, so each is retried on its own and a healthy hook is never
// sent again because another failed (#448). Hook selection is exactly
// hookSubscribes, as before.
//
// The per-hook row ids derive from the fan-out row's id, and the outbox
// ignores a repeated id, so running this again after a crash (between queueing
// and settling) reuses the same delivery ids. That stops a row still waiting
// from being queued twice; a row already sent and deleted would be queued
// again, with the same X-GHD-Delivery, which a receiver can drop.
//
// Each row holds the event as that hook needs it (rowFor): never an address,
// and for a raw hook no content at all.
func (d *WebhookDispatcher) fanOut(ctx context.Context, ev notification.Event) error {
	hooks, err := d.store.ListEnabledWebhooks(ctx)
	if err != nil {
		// Unlike the unqueued path this is worth failing: the worker tries
		// again, where there it meant zero deliveries (#213).
		return fmt.Errorf("listing enabled webhooks: %w", err)
	}
	parent := RowID(ctx)
	var first error
	for _, hook := range hooks {
		if !hookSubscribes(hook, ev.Type) {
			continue
		}
		// The ticket filter (#413) is applied here, once, and nowhere later: a
		// hook it turns away gets no row, so no delivery id, no retry and no
		// history entry. A row that was queued has been admitted, and its
		// retries, like Redeliver, do not judge the filter again.
		if !d.admits(ctx, hook, ev) {
			continue
		}
		perHook := rowFor(ev, hook)
		perHook.TargetWebhook = hook.ID
		perHook.TargetURLHash = urlHash(hook.URL)
		row, err := encodeEvent(perHook)
		if err != nil {
			first = errors.Join(first, fmt.Errorf("encoding event for webhook %s: %w", hook.ID, err))
			continue
		}
		id := uuid.New()
		if parent != uuid.Nil {
			id = uuid.NewSHA1(parent, hook.ID[:])
		}
		if err := d.queue.Outbox.Enqueue(ctx, id, "webhook", row); err != nil {
			first = errors.Join(first, err)
		}
	}
	if d.queue.Wake != nil {
		d.queue.Wake()
	}
	return first
}

// deliverQueued makes one attempt at one hook's row. nil settles the row
// (delivered, or not worth another try); an error has the worker try again
// on its schedule, or give up after the last attempt.
func (d *WebhookDispatcher) deliverQueued(ctx context.Context, ev notification.Event) error {
	hook, err := d.queue.Hooks.GetWebhook(ctx, ev.TargetWebhook)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		return nil // deleted since the event was queued
	case err != nil:
		return fmt.Errorf("loading webhook: %w", err)
	}
	if !hook.Enabled {
		return nil
	}
	// Settled, not failed, like a disabled or deleted hook: an administrator
	// who removed this event from the hook, or pointed it somewhere else, did
	// not ask for the queued copy to follow. Checked on every attempt, so it
	// stops retries too. Only the URL is fingerprinted: the current secret is
	// used, so a receiver that rotated its key gets the retry signed with the
	// new one.
	if !hookSubscribes(hook, ev.Type) {
		return nil
	}
	if ev.TargetURLHash != urlHash(hook.URL) {
		d.log.InfoContext(ctx, "webhook delivery dropped: the hook's URL changed since it was queued",
			"webhook_id", hook.ID, "event", ev.Type)
		return nil
	}
	raw, err := rawBody(ev)
	if err != nil {
		d.log.ErrorContext(ctx, "webhook skipped: event could not be marshalled",
			"webhook_id", hook.ID, "event", ev.Type, "error", err)
		return nil // the same bytes would fail every time
	}
	body, err := bodyFor(hook, ev, raw, d.baseURL)
	if err != nil {
		d.log.WarnContext(ctx, "webhook skipped: payload could not be built",
			"webhook_id", hook.ID, "payload_format", hook.PayloadFormat,
			"event", ev.Type, "error", err)
		return nil
	}

	// The row's id is the delivery id: it does not change between attempts.
	deliveryID := RowID(ctx)
	if deliveryID == uuid.Nil {
		deliveryID = uuid.New()
	}
	res, retryAfter := d.post(ctx, hook, body, deliveryID, ev.Type)
	d.record(hook, res)

	retry := shouldRetry(res)
	d.recordAttempt(hook, deliveryID, ev, res, retry && !LastAttempt(ctx))
	if !retry {
		return nil // settled: the row is deleted, nothing is kept
	}
	if LastAttempt(ctx) {
		d.scrubFailedRow(ctx, ev)
	}
	// A class and a status, never a message: this is what lands in last_error.
	err = fmt.Errorf("webhook delivery failed: %s (status %d)", res.Error, res.Status)
	if retryAfter > 0 {
		err = &notification.RetryAfterError{Err: err, After: retryAfter}
	}
	return err
}

// shouldRetry reports whether another attempt could plausibly do better:
// connection errors, timeouts, 5xx and 429. A refused address, any other
// status and a request that could not be built would fail the same way again.
func shouldRetry(r authstore.WebhookDelivery) bool {
	switch r.Error {
	case DeliveryTimeout, DeliveryDNS, DeliveryTLS, DeliveryConnection:
		return true
	case DeliveryHTTPStatus:
		return r.Status >= 500 || r.Status == http.StatusTooManyRequests
	}
	return false
}

// parseRetryAfter reads a Retry-After header: seconds, or an HTTP date. Zero
// when absent, malformed or already past.
func parseRetryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	// Capped here as well as by the worker: a count of seconds too large for a
	// Duration would wrap to a small or negative one on multiplication.
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs >= int64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(max(secs, 0)) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		return min(max(at.Sub(now), 0), maxRetryAfter)
	}
	return 0
}

// Redeliver queues the event of an earlier delivery to the same hook again,
// as a new delivery with a new id: a receiver that de-duplicates by id would
// otherwise drop the replay, which is the opposite of what was asked.
func (d *WebhookDispatcher) Redeliver(ctx context.Context, hookID, deliveryID uuid.UUID) error {
	if d.queue == nil {
		return errors.New("webhook delivery is not queued")
	}
	hook, err := d.queue.Hooks.GetWebhook(ctx, hookID)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		return ErrWebhookNotFound
	case err != nil:
		return fmt.Errorf("loading webhook: %w", err)
	}
	if !hook.Enabled {
		return ErrWebhookDisabled
	}
	stored, err := d.queue.History.GetWebhookDeliveryEvent(ctx, hookID, deliveryID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrDeliveryNotFound
	case err != nil:
		return fmt.Errorf("loading delivery: %w", err)
	}
	ev, err := decodeEvent(stored)
	if err != nil {
		return fmt.Errorf("reading stored event: %w", err)
	}
	// Refused up front, with a reason, rather than queued and then dropped at
	// send time where nobody would see why.
	if !hookSubscribes(hook, ev.Type) {
		return ErrWebhookNotSubscribed
	}
	// A chat or ITSM message is rebuilt from the ticket as it is now, because the
	// kept copy has no content. A raw hook gets the kept copy as it is.
	if !isRaw(hook) {
		if ev, err = d.rebuild(ctx, ev); err != nil {
			return err
		}
	}
	ev.TargetWebhook = hookID
	ev.TargetURLHash = urlHash(hook.URL)
	row, err := encodeEvent(ev)
	if err != nil {
		return fmt.Errorf("encoding event: %w", err)
	}
	if err := d.queue.Outbox.Enqueue(ctx, uuid.New(), "webhook", row); err != nil {
		return err
	}
	if d.queue.Wake != nil {
		d.queue.Wake()
	}
	return nil
}

// rebuild fills a kept event with the ticket as it is now: the current subject,
// tracking number and status name, the priority of a creation, and the text of
// the reply it announced only if that reply still exists and is public. A note
// never gets text, and its reply is not even looked up. A ticket that is gone
// leaves the event as it is, so the message is its headline only.
func (d *WebhookDispatcher) rebuild(ctx context.Context, ev notification.Event) (notification.Event, error) {
	if d.queue.Source == nil {
		return ev, nil
	}
	facts, found, err := d.queue.Source.TicketFacts(ctx, ev.TicketID)
	if err != nil {
		return ev, fmt.Errorf("reading ticket for redelivery: %w", err)
	}
	if !found {
		return ev, nil
	}
	ev.Subject, ev.TrackingNumber, ev.StatusName = facts.Subject, facts.TrackingNumber, facts.StatusName
	ev.Payload = maps.Clone(ev.Payload)
	if ev.Payload == nil {
		ev.Payload = map[string]any{}
	}
	switch ev.Type {
	case notification.EventTicketCreated:
		ev.Payload["Priority"] = facts.Priority
	case notification.EventTicketReplied:
		ev.Payload["internal"] = false
		raw, _ := ev.Payload["reply_id"].(string)
		replyID, perr := uuid.Parse(raw)
		if perr != nil {
			break
		}
		body, ok, err := d.queue.Source.PublicReplyBody(ctx, ev.TicketID, replyID)
		if err != nil {
			return ev, fmt.Errorf("reading reply for redelivery: %w", err)
		}
		if ok {
			ev.Payload["ReplyBody"] = body
		}
	case notification.EventTicketNoteAdded:
		ev.Payload["internal"] = true
	}
	return ev, nil
}

// scrubFailedRow clears a chat or ITSM row's content once its delivery has
// finally failed. The worker keeps a failed row for 30 days, and by then the
// message it was built for is never going to be sent. Ids stay.
func (d *WebhookDispatcher) scrubFailedRow(ctx context.Context, ev notification.Event) {
	rw, ok := d.queue.Outbox.(EventRewriter)
	id := RowID(ctx)
	if !ok || id == uuid.Nil {
		return
	}
	scrubbed, err := encodeEvent(historyEvent(ev))
	if err == nil {
		err = rw.RewriteEvent(ctx, id, scrubbed)
	}
	if err != nil {
		d.log.WarnContext(ctx, "webhook row kept with its content after the last failed attempt",
			"webhook_id", ev.TargetWebhook, "error", err)
	}
}

// Signature headers on outbound webhooks. Both carry the same HMAC-SHA256
// value while consumers migrate.
const (
	// SignatureHeader is the header to verify.
	SignatureHeader = "X-GHD-Signature"

	// LegacySignatureHeader predates the rename from Open Help Desk. Kept so
	// existing consumers keep validating; remove in a future release once
	// they have moved to SignatureHeader.
	LegacySignatureHeader = "X-OHD-Signature"
)

// Delivery headers (#448). X-GHD-Delivery names one delivery and is the same
// on every retry of it, so a receiver can drop a duplicate; X-GHD-Event is the
// event type, so it can route without parsing the body.
const (
	DeliveryHeader = "X-GHD-Delivery"
	EventHeader    = "X-GHD-Event"
)

// send makes one unnamed attempt; see post.
func (d *WebhookDispatcher) send(hook authstore.WebhookConfig, payload []byte) authstore.WebhookDelivery {
	res, _ := d.post(context.Background(), hook, payload, uuid.New(), "")
	return res
}

// post makes one attempt and returns its result, and how long the receiver
// asked us to wait (Retry-After) when it refused with a status.
func (d *WebhookDispatcher) post(ctx context.Context, hook authstore.WebhookConfig, payload []byte,
	deliveryID uuid.UUID, eventType notification.EventType) (authstore.WebhookDelivery, time.Duration) {
	at := time.Now().UTC()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(payload))
	if err != nil {
		d.log.Warn("webhook request could not be built", "webhook_id", hook.ID)
		return authstore.WebhookDelivery{At: at, Error: DeliveryOther}, 0
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(DeliveryHeader, deliveryID.String())
	if eventType != "" {
		req.Header.Set(EventHeader, string(eventType))
	}
	if hook.Secret != "" {
		sig := hmacSHA256(hook.Secret, payload)

		// Both headers, same signature, during the rename.
		//
		// A consumer verifying X-OHD-Signature does not fail loudly when the
		// header disappears — it keeps receiving webhooks and starts rejecting
		// every one of them as unsigned. There is no error on this side and no
		// obvious cause on theirs, so sending only the new name would be a
		// silent breakage discovered days later.
		//
		// SignatureHeader is the one to verify. LegacySignatureHeader is
		// deprecated and should be removed once consumers have moved; it is a
		// duplicate of the same value, not a second scheme.
		req.Header.Set(SignatureHeader, "sha256="+sig)
		req.Header.Set(LegacySignatureHeader, "sha256="+sig)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		d.log.Warn("webhook delivery failed", "webhook_id", hook.ID, "payload_format", hook.PayloadFormat, "error", withoutURL(err))
		return authstore.WebhookDelivery{At: at, Error: classifyDeliveryError(err)}, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		d.log.Warn("webhook delivery rejected", "webhook_id", hook.ID, "payload_format", hook.PayloadFormat, "status", resp.StatusCode)
		// The DB CHECK constraint only allows 0 or 100..999. Go's HTTP client
		// accepts any 3-digit status (e.g., "099" parses as 99, "000" as 0).
		// Store 0 for out-of-range values to keep the record valid.
		status := resp.StatusCode
		if status < 100 || status > 999 {
			status = 0
		}
		return authstore.WebhookDelivery{At: at, Status: status, Error: DeliveryHTTPStatus},
			parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	}
	return authstore.WebhookDelivery{At: at, Status: resp.StatusCode}, 0
}

func hookSubscribes(hook authstore.WebhookConfig, eventType notification.EventType) bool {
	if len(hook.Events) == 0 {
		return true // empty = all events
	}
	for _, e := range hook.Events {
		if e == string(eventType) || e == "*" {
			return true
		}
	}
	return false
}

// classifyDeliveryError categorizes a delivery error into one of the delivery
// error classes. The order matters: a blocked address and a DNS failure both
// arrive inside a *net.OpError, and a timeout can arrive as anything that
// implements net.Error.
func classifyDeliveryError(err error) string {
	var ne net.Error
	var dns *net.DNSError
	var cv *tls.CertificateVerificationError
	var rh tls.RecordHeaderError
	var op *net.OpError
	switch {
	case errors.Is(err, safehttp.ErrBlockedAddress):
		return DeliveryBlockedAddress
	case errors.As(err, &ne) && ne.Timeout():
		return DeliveryTimeout
	case errors.As(err, &dns):
		return DeliveryDNS
	case errors.As(err, &cv), errors.As(err, &rh):
		return DeliveryTLS
	case errors.As(err, &op), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return DeliveryConnection
	}
	return DeliveryOther
}

// record stores a delivery's result. Its own short timeout and a background
// context: the outbox row that started this delivery is already settled.
// A failure to record is logged and dropped; it must not affect delivery.
func (d *WebhookDispatcher) record(hook authstore.WebhookConfig, r authstore.WebhookDelivery) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.store.RecordWebhookDelivery(ctx, hook.ID, hook.URL, r); err != nil {
		d.log.Warn("webhook delivery result not recorded", "webhook_id", hook.ID, "error", err)
	}
}

// recordAttempt adds an attempt to the hook's delivery history. Like record:
// its own short timeout, and a failure is logged and dropped.
func (d *WebhookDispatcher) recordAttempt(hook authstore.WebhookConfig, deliveryID uuid.UUID,
	ev notification.Event, r authstore.WebhookDelivery, retrying bool) {
	stored, err := encodeEvent(historyEvent(ev))
	if err != nil {
		d.log.Warn("webhook delivery history not recorded", "webhook_id", hook.ID, "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.queue.History.RecordWebhookAttempt(ctx, authstore.WebhookAttempt{
		DeliveryID: deliveryID, HookID: hook.ID, EventType: string(ev.Type), Event: stored,
		Result: r, Retrying: retrying,
	}); err != nil {
		d.log.Warn("webhook delivery history not recorded", "webhook_id", hook.ID, "error", err)
	}
}

func hmacSHA256(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// withoutURL removes the URL that http.Client wraps around every error.
// The URL in *url.Error can contain credentials, so we unwrap it before logging.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
