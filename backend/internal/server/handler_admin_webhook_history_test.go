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
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

func historyHook(t *testing.T, h *harness, enabled bool) authstore.WebhookConfig {
	t.Helper()
	wh := authstore.WebhookConfig{ID: uuid.New(), URL: "https://example.com/tok-en", Events: []string{"*"},
		Secret: "sekrit", Enabled: enabled, CreatedAt: time.Now().UTC()}
	require.NoError(t, h.authStore.CreateWebhook(context.Background(), wh))
	return wh
}

func recordDelivery(t *testing.T, h *harness, hook authstore.WebhookConfig, at time.Time, status int, class string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, h.authStore.RecordWebhookAttempt(context.Background(), authstore.WebhookAttempt{
		DeliveryID: id, HookID: hook.ID, EventType: "ticket.replied",
		Event:  []byte(`{"Type":"ticket.replied","Payload":{"ReplyBody":"the reply text"},"webhook_id":"` + hook.ID.String() + `"}`),
		Result: authstore.WebhookDelivery{At: at, Status: status, Error: class},
	}))
	return id
}

func queuedWebhookRows(t *testing.T, h *harness) int {
	t.Helper()
	var n int
	require.NoError(t, h.tx.QueryRow(`SELECT count(*) FROM notification_outbox WHERE channel = 'webhook'`).Scan(&n))
	return n
}

// The history shows outcomes and nothing else: no event, no body, no error
// text, and never the secret or the URL that may carry a token.
func TestWebhookHistory_ListShowsOutcomesOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	hook := historyHook(t, h, true)
	now := time.Now().UTC()
	older := recordDelivery(t, h, hook, now.Add(-time.Minute), 200, "")
	newer := recordDelivery(t, h, hook, now, 503, "http_status")

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/webhooks/"+hook.ID.String()+"/deliveries", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var raw json.RawMessage
	require.NoError(t, json.NewDecoder(res.Body).Decode(&raw))
	for _, leak := range []string{"the reply text", "sekrit", "tok-en", "webhook_id", "ReplyBody"} {
		require.NotContains(t, string(raw), leak)
	}

	var list []map[string]any
	require.NoError(t, json.Unmarshal(raw, &list))
	require.Len(t, list, 2)
	require.Equal(t, newer.String(), list[0]["id"], "newest first")
	require.Equal(t, older.String(), list[1]["id"])
	require.ElementsMatch(t,
		[]string{"id", "event", "started_at", "last_attempt_at", "status", "result", "attempts", "retrying"},
		keysOf(list[0]))
	require.Equal(t, "http_status", list[0]["result"])
	require.Equal(t, float64(503), list[0]["status"])
	require.Equal(t, "ok", list[1]["result"])
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestWebhookHistory_EmptyIsAnEmptyList(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	hook := historyHook(t, h, true)

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/webhooks/"+hook.ID.String()+"/deliveries", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var raw json.RawMessage
	require.NoError(t, json.NewDecoder(res.Body).Decode(&raw))
	require.JSONEq(t, `[]`, string(raw), "[] and not null, so a client can map over it")
}

func TestWebhookHistory_UnknownHookIs404(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/webhooks/"+uuid.NewString()+"/deliveries", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestWebhookHistory_NotForStaff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	hook := historyHook(t, h, true)
	id := recordDelivery(t, h, hook, time.Now().UTC(), 200, "")

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/webhooks/" + hook.ID.String() + "/deliveries"},
		{http.MethodPost, "/api/v1/admin/webhooks/" + hook.ID.String() + "/deliveries/" + id.String() + "/redeliver"},
	} {
		res := h.do(t, tc.method, tc.path, nil) // the staff key
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode, tc.method+" "+tc.path)
	}
	require.Equal(t, 0, queuedWebhookRows(t, h))
}

func TestWebhookRedeliver_QueuesTheEventAgain(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	hook := historyHook(t, h, true)
	id := recordDelivery(t, h, hook, time.Now().UTC(), 503, "http_status")
	before := queuedWebhookRows(t, h)

	res := h.doAsAdmin(t, http.MethodPost,
		"/api/v1/admin/webhooks/"+hook.ID.String()+"/deliveries/"+id.String()+"/redeliver", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	require.Equal(t, before+1, queuedWebhookRows(t, h))
	var target string
	require.NoError(t, h.tx.QueryRow(
		`SELECT event->>'webhook_id' FROM notification_outbox WHERE channel = 'webhook' ORDER BY created_at DESC LIMIT 1`).Scan(&target))
	require.Equal(t, hook.ID.String(), target, "the replay is addressed to the one hook")
}

// A hook that no longer lists the delivery's event is told so, with a reason,
// and nothing is queued.
func TestWebhookRedeliver_RefusesAnEventTheHookDroppedWith409(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	hook := historyHook(t, h, true)
	id := recordDelivery(t, h, hook, time.Now().UTC(), 200, "") // a ticket.replied delivery
	hook.Events = []string{"ticket.closed"}
	require.NoError(t, h.authStore.UpdateWebhook(context.Background(), hook))

	res := h.doAsAdmin(t, http.MethodPost,
		"/api/v1/admin/webhooks/"+hook.ID.String()+"/deliveries/"+id.String()+"/redeliver", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode)
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "webhook_not_subscribed", body.Error.Code)
	require.Equal(t, 0, queuedWebhookRows(t, h))
}

// A hook whose filter, as it is now, does not admit the ticket the delivery
// was about is told so, with a reason, and nothing is queued (#413). The
// ticket's attributes are read from the ticket: the kept copy has none.
func TestWebhookRedeliver_RechecksTheFilterAgainstTheTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, _ := seedGuestTicket(t, h)
	other := "critical"
	if string(tk.Priority) == other {
		other = "low"
	}

	for _, tc := range []struct {
		name       string
		priorities []string
		want       int
		code       string
	}{
		{"does not match", []string{other}, http.StatusConflict, "webhook_filter_excludes"},
		{"matches", []string{string(tk.Priority)}, http.StatusAccepted, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := historyHook(t, h, true)
			hook.Filter = notification.WebhookFilter{Priorities: tc.priorities}
			require.NoError(t, h.authStore.UpdateWebhook(context.Background(), hook))
			id := uuid.New()
			require.NoError(t, h.authStore.RecordWebhookAttempt(context.Background(), authstore.WebhookAttempt{
				DeliveryID: id, HookID: hook.ID, EventType: "ticket.replied",
				Event:  []byte(`{"type":"ticket.replied","ticket_id":"` + tk.ID.String() + `","webhook_id":"` + hook.ID.String() + `"}`),
				Result: authstore.WebhookDelivery{At: time.Now().UTC(), Status: 200},
			}))
			before := queuedWebhookRows(t, h)

			res := h.doAsAdmin(t, http.MethodPost,
				"/api/v1/admin/webhooks/"+hook.ID.String()+"/deliveries/"+id.String()+"/redeliver", nil)
			defer res.Body.Close()
			require.Equal(t, tc.want, res.StatusCode)
			if tc.code == "" {
				require.Equal(t, before+1, queuedWebhookRows(t, h))
				return
			}
			var body struct {
				Error struct{ Code string } `json:"error"`
			}
			require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
			require.Equal(t, tc.code, body.Error.Code)
			require.Equal(t, before, queuedWebhookRows(t, h))
		})
	}
}

func TestWebhookRedeliver_Refusals(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	on := historyHook(t, h, true)
	off := historyHook(t, h, false)
	onDelivery := recordDelivery(t, h, on, time.Now().UTC(), 200, "")
	offDelivery := recordDelivery(t, h, off, time.Now().UTC(), 200, "")

	for _, tc := range []struct {
		name string
		path string
		want int
	}{
		{"unknown delivery", on.ID.String() + "/deliveries/" + uuid.NewString(), http.StatusNotFound},
		{"unknown hook", uuid.NewString() + "/deliveries/" + onDelivery.String(), http.StatusNotFound},
		{"another hook's delivery", on.ID.String() + "/deliveries/" + offDelivery.String(), http.StatusNotFound},
		{"disabled hook", off.ID.String() + "/deliveries/" + offDelivery.String(), http.StatusConflict},
		{"bad delivery id", on.ID.String() + "/deliveries/nope", http.StatusBadRequest},
		{"bad hook id", "nope/deliveries/" + onDelivery.String(), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks/"+tc.path+"/redeliver", nil)
			res.Body.Close()
			require.Equal(t, tc.want, res.StatusCode)
		})
	}
	require.Equal(t, 0, queuedWebhookRows(t, h), "a refusal must queue nothing")
}
