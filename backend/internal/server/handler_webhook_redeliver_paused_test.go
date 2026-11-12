package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A replay on a hook that is paused (its API key died) used to answer "this
// webhook is disabled; enable it to redeliver". Enabling does nothing for a
// paused hook: only a signed-in administrator re-approving it lifts the pause,
// so the message sent the administrator to a switch that was already on.
func TestWebhookRedeliver_APausedHookSaysItIsPausedAndNotDisabled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	raw, keyID := h.mintAdminKey(t, "webhooks:write", "webhooks:read")
	id := createHookWith(t, h, func(m, p string, b any) *http.Response { return keyDo(t, h, raw, m, p, b) })
	hook := h.hook(t, id)
	delivery := recordDelivery(t, h, hook, time.Now().UTC(), 200, "")

	require.NoError(t, h.authStore.Delete(context.Background(), keyID))
	require.True(t, h.hook(t, id).Paused)

	res := h.doAsAdmin(t, http.MethodPost, hooksPath+"/"+id.String()+"/deliveries/"+delivery.String()+"/redeliver", nil)
	body := readAll(t, res)
	require.Equal(t, http.StatusConflict, res.StatusCode, string(body))
	var e struct {
		Error struct{ Code, Message string }
	}
	require.NoError(t, json.Unmarshal(body, &e))
	require.Equal(t, "webhook_paused", e.Error.Code)
	require.Contains(t, e.Error.Message, "re-approve")
	require.NotContains(t, e.Error.Message, "disabled")
	require.Equal(t, 0, queuedWebhookRows(t, h), "a refusal must queue nothing")
}
