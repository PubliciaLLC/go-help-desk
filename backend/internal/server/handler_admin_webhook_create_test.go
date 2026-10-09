package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Omitted and an explicit null both mean "enabled", which is what create did
// before the field existed. Null is pinned on its own: a decoder that tells a
// present-but-null field apart from a missing one would otherwise be free to
// read it as false.
func TestWebhookCreate_EnabledDefaultsToTrue(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"omitted", map[string]any{"url": "https://example.com/hook", "events": []string{"*"}}},
		{"null", map[string]any{"url": "https://example.com/hook", "events": []string{"*"}, "enabled": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()

			res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks", tc.body)
			defer res.Body.Close()
			require.Equal(t, http.StatusCreated, res.StatusCode)

			var created struct {
				ID      string `json:"id"`
				Enabled bool   `json:"enabled"`
			}
			require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
			require.True(t, created.Enabled, "enabled must default to true in response")

			stored, err := h.authStore.GetWebhook(context.Background(), uuid.MustParse(created.ID))
			require.NoError(t, err)
			require.True(t, stored.Enabled, "enabled must default to true in storage")
		})
	}
}

func TestWebhookCreate_EnabledFalseIsStoredDisabled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks",
		map[string]any{"url": "https://example.com/hook", "events": []string{"*"}, "enabled": false})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	require.False(t, created.Enabled, "enabled false must be returned in response")

	stored, err := h.authStore.GetWebhook(context.Background(), uuid.MustParse(created.ID))
	require.NoError(t, err)
	require.False(t, stored.Enabled, "enabled false must be stored")

	enabled, err := h.authStore.ListEnabledWebhooks(context.Background())
	require.NoError(t, err)
	for _, w := range enabled {
		require.NotEqual(t, stored.ID, w.ID, "disabled webhook must not appear in ListEnabledWebhooks")
	}
}

func TestWebhookCreate_EnabledMustBeBoolean(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Count webhooks before
	beforeList, err := h.authStore.ListWebhooks(context.Background())
	require.NoError(t, err)
	countBefore := len(beforeList)

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks",
		map[string]any{"url": "https://example.com/hook", "events": []string{"*"}, "enabled": "no"})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "bad_request", body.Error.Code)

	// Count webhooks after - should be unchanged
	afterList, err := h.authStore.ListWebhooks(context.Background())
	require.NoError(t, err)
	require.Equal(t, countBefore, len(afterList), "rejected create must not store a webhook")
}
