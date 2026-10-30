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
)

// The admin list is the admin's view of every subscription, not the
// dispatcher's view of the ones that fire. A hook that has been switched off
// must still be listed: it is the only place it can be switched back on, and
// a list that silently drops it makes "disable" indistinguishable from
// "delete" (#157).
func TestWebhookList_IncludesDisabledHooksAndNeverEchoesSecrets(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	on, off := uuid.New(), uuid.New()
	for _, wh := range []authstore.WebhookConfig{
		{ID: on, URL: "https://example.com/on", Events: []string{"ticket.created"}, Secret: "sekrit-on", Enabled: true, CreatedAt: time.Now()},
		{ID: off, URL: "https://example.com/off", Events: []string{"*"}, Secret: "sekrit-off", Enabled: false, CreatedAt: time.Now().Add(time.Second), PayloadFormat: "slack"},
	} {
		require.NoError(t, h.authStore.CreateWebhook(context.Background(), wh))
	}

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/webhooks", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var raw json.RawMessage
	require.NoError(t, json.NewDecoder(res.Body).Decode(&raw))
	require.NotContains(t, string(raw), "sekrit", "a webhook secret is write-only and must never be returned")

	var list []struct {
		ID            string `json:"id"`
		Enabled       bool   `json:"enabled"`
		PayloadFormat string `json:"payload_format"`
	}
	require.NoError(t, json.Unmarshal(raw, &list))

	got := map[string]bool{}
	for _, w := range list {
		got[w.ID] = w.Enabled
	}
	require.Contains(t, got, on.String())
	require.Contains(t, got, off.String(), "a disabled webhook must still be listed so it can be re-enabled")
	require.True(t, got[on.String()])
	require.False(t, got[off.String()])
}

// The dispatcher's own query is unchanged: a disabled hook is listed for the
// admin but never delivered to.
func TestWebhookList_DispatcherStillSeesOnlyEnabledHooks(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	off := uuid.New()
	require.NoError(t, h.authStore.CreateWebhook(context.Background(), authstore.WebhookConfig{
		ID: off, URL: "https://example.com/off", Events: []string{"*"}, Enabled: false, CreatedAt: time.Now(),
	}))

	enabled, err := h.authStore.ListEnabledWebhooks(context.Background())
	require.NoError(t, err)
	for _, w := range enabled {
		require.NotEqual(t, off, w.ID)
	}
}
