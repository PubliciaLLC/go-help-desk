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

// Creating a webhook checks the target address. Updating one did not, so a hook
// could be repointed at the LAN and accepted with 200.
//
// The guarded dialer still refused the address at delivery, so nothing was ever
// fetched — that is the real boundary and it held either way. What the missing
// check cost was the telling: the hook is stored, shown as enabled, and never
// delivers, and delivery failures are dropped with no log and no record, so an
// administrator cannot tell it from a working one.
//
// Which addresses are refused is safehttp's question and is tested there. This
// asks only whether the update path runs that check at all, so it names no
// address: it seeds a hook through the store, bypassing create, then asks the
// handler to repoint it at a host every machine resolves to loopback.
func seedWebhook(t *testing.T, h *harness) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, h.authStore.CreateWebhook(context.Background(), authstore.WebhookConfig{
		ID:        id,
		URL:       "https://example.invalid/hook",
		Events:    []string{"ticket.created"},
		Enabled:   true,
		CreatedAt: time.Now(),
	}))
	return id
}

func TestWebhookUpdate_ChecksTheTargetAddressAsCreateDoes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := seedWebhook(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"url": "http://localhost:9200/hook"})
	defer res.Body.Close()

	require.Equal(t, http.StatusBadRequest, res.StatusCode,
		"repointing a hook at an address create would refuse must be refused too")

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "invalid_url", body.Error.Code,
		"and with the same code as create, so a caller handles one case")

	// A refused update must not half-apply.
	stored, err := h.authStore.GetWebhook(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "https://example.invalid/hook", stored.URL)
}

// The check gates the URL and nothing else: an update that does not touch the
// address still succeeds, even though the stored address would not pass today.
func TestWebhookUpdate_OtherFieldsAreNotGatedOnTheAddress(t *testing.T) {
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
}
