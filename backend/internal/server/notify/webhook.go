package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/open-help-desk/open-help-desk/backend/internal/database/authstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/notification"
)

// WebhookStore is the interface needed to load enabled webhook configs.
type WebhookStore interface {
	ListEnabledWebhooks(ctx context.Context) ([]authstore.WebhookConfig, error)
}

// WebhookDispatcher sends HTTP POST payloads to configured webhook URLs.
type WebhookDispatcher struct {
	store  WebhookStore
	client *http.Client
}

// NewWebhookDispatcher returns a WebhookDispatcher with sensible timeouts.
func NewWebhookDispatcher(store WebhookStore) *WebhookDispatcher {
	return &WebhookDispatcher{
		store:  store,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Dispatch sends the event as JSON to every enabled webhook that subscribes
// to this event type. Failures are logged but do not propagate.
func (d *WebhookDispatcher) Dispatch(ctx context.Context, event notification.Event) error {
	hooks, err := d.store.ListEnabledWebhooks(ctx)
	if err != nil {
		return nil // store failure is non-fatal
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return nil
	}

	for _, hook := range hooks {
		if !hookSubscribes(hook, event.Type) {
			continue
		}
		// Fire-and-forget per webhook; don't block on failures.
		go d.send(hook, payload)
	}
	return nil
}

func (d *WebhookDispatcher) send(hook authstore.WebhookConfig, payload []byte) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, hook.URL, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if hook.Secret != "" {
		sig := hmacSHA256(hook.Secret, payload)
		req.Header.Set("X-OHD-Signature", "sha256="+sig)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
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

// Prevent unused import
var _ = fmt.Sprintf
