package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/publiciallc/go-help-desk/backend/internal/safehttp"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
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

// WebhookDispatcher sends HTTP POST payloads to configured webhook URLs.
type WebhookDispatcher struct {
	store   WebhookStore
	client  *http.Client
	baseURL string // used only to build the staff ticket link chat/ITSM formats carry
	log     *slog.Logger
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

	hooks, err := d.store.ListEnabledWebhooks(ctx)
	if err != nil {
		// A DB error listing hooks means zero deliveries to every webhook;
		// previously this was indistinguishable from "nothing subscribed".
		// See #213.
		d.log.ErrorContext(ctx, "webhook dispatch skipped: could not list enabled webhooks",
			"event", event.Type, "error", err)
		return nil // store failure is non-fatal
	}

	// Marshalled once, exactly as before: this is the raw wire body every
	// "raw" (and pre-migration empty-format) subscription still receives
	// byte-for-byte unchanged. bodyFor below reshapes a copy per format; it
	// never touches this slice for those hooks.
	payload, err := json.Marshal(event)
	if err != nil {
		d.log.ErrorContext(ctx, "webhook dispatch skipped: event could not be marshalled",
			"event", event.Type, "error", err)
		return nil
	}

	for _, hook := range hooks {
		if !hookSubscribes(hook, event.Type) {
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
			d.record(hook, d.send(hook, body))
		}(hook, body)
	}
	return nil
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

func (d *WebhookDispatcher) send(hook authstore.WebhookConfig, payload []byte) authstore.WebhookDelivery {
	at := time.Now().UTC()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, hook.URL, bytes.NewReader(payload))
	if err != nil {
		d.log.Warn("webhook request could not be built", "webhook_id", hook.ID)
		return authstore.WebhookDelivery{At: at, Error: DeliveryOther}
	}
	req.Header.Set("Content-Type", "application/json")
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
		return authstore.WebhookDelivery{At: at, Error: classifyDeliveryError(err)}
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
		return authstore.WebhookDelivery{At: at, Status: status, Error: DeliveryHTTPStatus}
	}
	// Retry logic for v2: for now, accept any 2xx.
	return authstore.WebhookDelivery{At: at, Status: resp.StatusCode}
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
