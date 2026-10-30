package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
)

// TestWebhookList_ShowsLastDelivery pins that the webhook list endpoint
// includes the last_delivery field showing the result of the latest delivery
// attempt, with null for hooks never delivered, and the full result (at, status,
// error) for hooks with delivery history (#157).
func TestWebhookList_ShowsLastDelivery(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	now := time.Now().UTC()
	hookA := authstore.WebhookConfig{
		ID:        uuid.New(),
		URL:       "https://example.com/a",
		Events:    []string{"ticket.created"},
		Secret:    "sekrit-a",
		Enabled:   true,
		CreatedAt: now,
	}
	hookB := authstore.WebhookConfig{
		ID:        uuid.New(),
		URL:       "https://example.com/b",
		Events:    []string{"*"},
		Secret:    "sekrit-b",
		Enabled:   true,
		CreatedAt: now.Add(time.Second),
	}
	hookC := authstore.WebhookConfig{
		ID:        uuid.New(),
		URL:       "https://example.com/c",
		Events:    []string{"ticket.resolved"},
		Secret:    "sekrit-c",
		Enabled:   true,
		CreatedAt: now.Add(2 * time.Second),
	}

	for _, wh := range []authstore.WebhookConfig{hookA, hookB, hookC} {
		require.NoError(t, h.authStore.CreateWebhook(context.Background(), wh))
	}

	// Record delivery results for hookB and hookC.
	require.NoError(t, h.authStore.RecordWebhookDelivery(context.Background(), hookB.ID, hookB.URL,
		authstore.WebhookDelivery{
			At:     now.Add(10 * time.Second),
			Status: 500,
			Error:  notify.DeliveryHTTPStatus,
		}))

	require.NoError(t, h.authStore.RecordWebhookDelivery(context.Background(), hookC.ID, hookC.URL,
		authstore.WebhookDelivery{
			At:     now.Add(5 * time.Second),
			Status: 0,
			Error:  notify.DeliveryTimeout,
		}))

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/webhooks", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var list []map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&list))

	// Find hooks by ID.
	hooksByID := make(map[string]map[string]any)
	for _, hook := range list {
		hooksByID[hook["id"].(string)] = hook
	}

	// Check hookA (never delivered): last_delivery is present and null.
	hookAData := hooksByID[hookA.ID.String()]
	require.NotNil(t, hookAData)
	require.Contains(t, hookAData, "last_delivery")
	require.Nil(t, hookAData["last_delivery"], "hook never delivered must have null last_delivery")

	// Check hookB (500 error): last_delivery has exact keys and values.
	hookBData := hooksByID[hookB.ID.String()]
	require.NotNil(t, hookBData)
	require.Contains(t, hookBData, "last_delivery")
	require.NotNil(t, hookBData["last_delivery"], "hookB should have a non-nil last_delivery")
	bDelivery, ok := hookBData["last_delivery"].(map[string]any)
	require.True(t, ok, "last_delivery should be a map")
	require.Len(t, bDelivery, 3, "last_delivery should have exactly 3 keys: at, status, error")
	require.Equal(t, float64(500), bDelivery["status"])
	require.Equal(t, notify.DeliveryHTTPStatus, bDelivery["error"])
	require.NotNil(t, bDelivery["at"])

	// Check hookC (timeout): last_delivery has status 0.
	hookCData := hooksByID[hookC.ID.String()]
	require.NotNil(t, hookCData)
	require.Contains(t, hookCData, "last_delivery")
	require.NotNil(t, hookCData["last_delivery"], "hookC should have a non-nil last_delivery")
	cDelivery, ok := hookCData["last_delivery"].(map[string]any)
	require.True(t, ok, "last_delivery should be a map")
	require.Len(t, cDelivery, 3)
	require.Equal(t, float64(0), cDelivery["status"])
	require.Equal(t, notify.DeliveryTimeout, cDelivery["error"])
	require.NotNil(t, cDelivery["at"])

	// Verify no secrets are in the raw body.
	// (The secret redaction is already tested in TestWebhookList_IncludesDisabledHooksAndNeverEchoesSecrets)
}

// TestWebhookCreate_ResponseHasNullLastDelivery pins that the 201 response
// from creating a webhook includes last_delivery with a nil value, since the
// hook has never been delivered to yet (#157).
func TestWebhookCreate_ResponseHasNullLastDelivery(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	body := map[string]any{
		"url":    "https://example.com/webhook",
		"events": []string{"ticket.created"},
	}

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks", body)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))

	require.Contains(t, created, "last_delivery")
	require.Nil(t, created["last_delivery"], "newly created hook must have null last_delivery")
}
