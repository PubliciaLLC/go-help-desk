package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/publiciallc/go-help-desk/backend/internal/safehttp"
	"log/slog"
	"net/http"
	"net/url"
	"time"

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

// WebhookStore is the interface needed to load enabled webhook configs.
type WebhookStore interface {
	ListEnabledWebhooks(ctx context.Context) ([]authstore.WebhookConfig, error)
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
	hooks, err := d.store.ListEnabledWebhooks(ctx)
	if err != nil {
		return nil // store failure is non-fatal
	}

	// Marshalled once, exactly as before: this is the raw wire body every
	// "raw" (and pre-migration empty-format) subscription still receives
	// byte-for-byte unchanged. bodyFor below reshapes a copy per format; it
	// never touches this slice for those hooks.
	payload, err := json.Marshal(event)
	if err != nil {
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
		// Fire-and-forget per webhook; don't block on failures.
		go d.send(hook, body)
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

func (d *WebhookDispatcher) send(hook authstore.WebhookConfig, payload []byte) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, hook.URL, bytes.NewReader(payload))
	if err != nil {
		d.log.Warn("webhook request could not be built", "webhook_id", hook.ID)
		return
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
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		d.log.Warn("webhook delivery rejected", "webhook_id", hook.ID, "payload_format", hook.PayloadFormat, "status", resp.StatusCode)
		return
	}
	// Retry logic for v2: for now, accept any 2xx.
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
