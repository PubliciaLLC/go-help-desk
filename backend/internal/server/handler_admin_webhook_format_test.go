package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
)

// A webhook's payload_format defaults to "raw" so every subscription that
// predates this field, and every create that omits it, keeps receiving
// exactly what it receives today.
func TestWebhookCreate_DefaultsPayloadFormatToRaw(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks",
		map[string]any{"url": "https://example.com/hook", "events": []string{"ticket.created"}})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created struct {
		ID            string `json:"id"`
		PayloadFormat string `json:"payload_format"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	require.Equal(t, "raw", created.PayloadFormat)

	stored, err := h.authStore.GetWebhook(context.Background(), uuid.MustParse(created.ID))
	require.NoError(t, err)
	require.Equal(t, "raw", stored.PayloadFormat)
}

func TestWebhookCreate_AcceptsAKnownPayloadFormat(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks",
		map[string]any{"url": "https://example.com/hook", "events": []string{"ticket.created"}, "payload_format": "slack"})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created struct {
		ID            string `json:"id"`
		PayloadFormat string `json:"payload_format"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	require.Equal(t, "slack", created.PayloadFormat)

	stored, err := h.authStore.GetWebhook(context.Background(), uuid.MustParse(created.ID))
	require.NoError(t, err)
	require.Equal(t, "slack", stored.PayloadFormat)
}

func TestWebhookCreate_RejectsAnUnknownPayloadFormat(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks",
		map[string]any{"url": "https://example.com/hook", "events": []string{"ticket.created"}, "payload_format": "xml"})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "invalid_payload_format", body.Error.Code)
}

func TestWebhookUpdate_ChangesPayloadFormat(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := seedWebhook(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"payload_format": "discord"})
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	stored, err := h.authStore.GetWebhook(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "discord", stored.PayloadFormat)
}

func TestWebhookUpdate_RejectsAnUnknownPayloadFormat(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := seedWebhook(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"payload_format": "xml"})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "invalid_payload_format", body.Error.Code)

	// A refused update must not half-apply.
	stored, err := h.authStore.GetWebhook(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "raw", stored.PayloadFormat)
}

// The check gates payload_format and nothing else: an update that does not
// touch it still succeeds, exactly as TestWebhookUpdate_OtherFieldsAreNotGatedOnTheAddress
// asserts for url.
func TestWebhookUpdate_OtherFieldsAreNotGatedOnPayloadFormat(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := seedWebhook(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"enabled": false})
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	stored, err := h.authStore.GetWebhook(context.Background(), id)
	require.NoError(t, err)
	require.False(t, stored.Enabled)
	require.Equal(t, "raw", stored.PayloadFormat, "unrelated to what this PATCH touched")
}

// A webhook subscription with zero events would silently never fire: a footgun.
// Require events to be present and non-empty.
func TestWebhookCreate_RejectsWithoutEvents(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks",
		map[string]any{"url": "https://example.com/hook"})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "missing_events", body.Error.Code)
}

func TestWebhookCreate_RejectsWithEmptyEvents(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks",
		map[string]any{"url": "https://example.com/hook", "events": []string{}})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "missing_events", body.Error.Code)
}

func TestWebhookCreate_AcceptsWithNonEmptyEvents(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks",
		map[string]any{"url": "https://example.com/hook", "events": []string{"ticket.created"}})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created struct {
		ID     string   `json:"id"`
		Events []string `json:"events"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	require.Equal(t, []string{"ticket.created"}, created.Events)
}

func TestWebhookUpdate_OmittingEventsDoesNotChangeIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := seedWebhook(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"enabled": false})
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	stored, err := h.authStore.GetWebhook(context.Background(), id)
	require.NoError(t, err)
	require.False(t, stored.Enabled)
	require.Equal(t, []string{"ticket.created"}, stored.Events)
}

func TestWebhookUpdate_RejectsWithEmptyEvents(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := seedWebhook(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"events": []string{}})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "missing_events", body.Error.Code)

	// A refused update must not half-apply.
	stored, err := h.authStore.GetWebhook(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, []string{"ticket.created"}, stored.Events)
}

// ── Event Validation ──────────────────────────────────────────────────────────

// The dispatcher's matcher compares strings exactly, so event names must be
// validated at creation and update time, not left to the dispatcher.
func TestWebhookCreate_RejectsUnknownEventName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cases := []struct {
		name   string
		events []string
	}{
		{name: "typo in event name", events: []string{"ticket.creatd"}},
		{name: "bad event in array", events: []string{"ticket.created", "bogus"}},
		{name: "empty string in array", events: []string{""}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks",
				map[string]any{"url": "https://example.com/hook", "events": tc.events})
			defer res.Body.Close()
			require.Equal(t, http.StatusBadRequest, res.StatusCode)

			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
			require.Equal(t, "invalid_event_name", body.Error.Code)
			// The message must name the bad value in %q form - find the bad value
			badValue := tc.events[0]
			if len(tc.events) > 1 {
				// For arrays with multiple values, find the first bad one
				for _, e := range tc.events {
					if e == "bogus" || e == "" {
						badValue = e
						break
					}
				}
			}
			require.Contains(t, body.Error.Message, fmt.Sprintf("%q", badValue))
		})
	}
}

func TestWebhookCreate_AcceptsEveryKnownEventName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Test with "*"
	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks",
		map[string]any{"url": "https://example.com/hook1", "events": []string{"*"}})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created1 struct {
		Events []string `json:"events"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created1))
	require.Equal(t, []string{"*"}, created1.Events)

	// Test with all known events from notify.WebhookEvents
	eventNames := make([]string, 0)
	for _, evt := range notify.WebhookEvents {
		eventNames = append(eventNames, string(evt))
	}

	res = h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks",
		map[string]any{"url": "https://example.com/hook2", "events": eventNames})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created2 struct {
		Events []string `json:"events"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created2))
	require.Equal(t, eventNames, created2.Events)
}

func TestWebhookUpdate_RejectsUnknownEventName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := seedWebhook(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"events": []string{"ticket.creatd"}})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	var body struct {
		Error struct{
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "invalid_event_name", body.Error.Code)

	// A refused update must not half-apply.
	stored, err := h.authStore.GetWebhook(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, []string{"ticket.created"}, stored.Events)
}

func TestWebhookUpdate_AcceptsKnownEventNames(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := seedWebhook(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"events": []string{"ticket.closed", "*"}})
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		Events []string `json:"events"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, []string{"ticket.closed", "*"}, body.Events)

	stored, err := h.authStore.GetWebhook(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, []string{"ticket.closed", "*"}, stored.Events)
}
