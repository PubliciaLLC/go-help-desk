package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// Creating and deleting an OAuth client, and creating, editing and deleting a
// webhook, are audited like an API key being issued or revoked: an entity type,
// an action per change, the signed-in administrator as the actor (for a key, its
// owner), and a before/after that never holds a secret. Unlike the key entries
// these are written in the same transaction as the change (see
// authstore_audit_tx_test.go for the rollback); here the harness joins one
// transaction, so these tests pin what the entries say.

const (
	clientsPath = "/api/v1/admin/oauth-clients"

	// The path and query of a webhook URL are where its token lives (Slack,
	// Discord and Teams all put it there). None of it may reach the audit log.
	hookURLToken  = "T0AAA/B0BBB/SECRETPATHTOKEN"
	hookURLQuery  = "QUERYTOKEN"
	hookURL       = "https://example.com/services/" + hookURLToken + "?token=" + hookURLQuery
	hookSigSecret = "signing-secret-value"
)

func (h *harness) entriesFor(t *testing.T, entityType string, id uuid.UUID) []audit.Entry {
	t.Helper()
	es, err := h.auditStore.ListByEntity(context.Background(), entityType, id, 50, 0)
	require.NoError(t, err)
	return es // newest first
}

// rawAudit is every stored before/after of an entity as one string, for the
// "this never appears" assertions: the view redacts, the row must be clean.
func (h *harness) rawAudit(t *testing.T, id uuid.UUID) string {
	t.Helper()
	rows, err := h.tx.Query(`SELECT coalesce(before::text, '') || coalesce(after::text, '') FROM audit_log WHERE entity_id = $1`, id)
	require.NoError(t, err)
	defer rows.Close()
	var all strings.Builder
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		all.WriteString(s)
	}
	require.NoError(t, rows.Err())
	return all.String()
}

func (h *harness) createClient(t *testing.T, name string, scopes ...string) (id uuid.UUID, userID uuid.UUID, secret string) {
	t.Helper()
	res := h.createCredential(t, clientsPath, map[string]any{"name": name, "scopes": scopes})
	b := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
	var out struct {
		ID           uuid.UUID `json:"id"`
		UserID       uuid.UUID `json:"user_id"`
		ClientSecret string    `json:"client_secret"`
	}
	require.NoError(t, json.Unmarshal(b, &out))
	return out.ID, out.UserID, out.ClientSecret
}

func TestOAuthClientAudit_CreateAndDelete(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id, userID, secret := h.createClient(t, "Nightly sync", "tickets:read", "tickets:write")

	es := h.entriesFor(t, "oauth_client", id)
	require.Len(t, es, 1)
	require.Equal(t, "oauth_client_created", es[0].Action)
	require.NotNil(t, es[0].ActorID)
	require.Equal(t, h.adminID, *es[0].ActorID, "the signed-in administrator who made it")
	require.Nil(t, es[0].Before)
	require.Equal(t, map[string]any{
		"client_name":     "Nightly sync",
		"scopes":          "tickets:read,tickets:write",
		"service_user_id": userID.String(),
	}, es[0].After)

	clients, err := h.authStore.ListOAuthClients(context.Background())
	require.NoError(t, err)
	var hashed string
	for _, c := range clients {
		if c.ID == id {
			hashed = c.HashedSecret
		}
	}
	require.NotEmpty(t, hashed)
	raw := h.rawAudit(t, id)
	require.NotContains(t, raw, secret, "the client secret is shown once and never stored")
	require.NotContains(t, raw, hashed, "nor its hash")

	res := h.doSession(t, h.session(t, asAdmin), http.MethodDelete, clientsPath+"/"+id.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	es = h.entriesFor(t, "oauth_client", id)
	require.Len(t, es, 2)
	require.Equal(t, "oauth_client_deleted", es[0].Action)
	require.Equal(t, h.adminID, *es[0].ActorID)
	require.Nil(t, es[0].After)
	require.Equal(t, map[string]any{
		"client_name":     "Nightly sync",
		"scopes":          "tickets:read,tickets:write",
		"service_user_id": userID.String(),
	}, es[0].Before, "a delete says what was removed")
	require.NotContains(t, h.rawAudit(t, id), hashed)
}

func TestOAuthClientAudit_RefusedRequestsWriteNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := h.session(t, asAdmin)
	count := func() (n int) {
		require.NoError(t, h.tx.QueryRow(`SELECT count(*) FROM audit_log WHERE entity_type = 'oauth_client'`).Scan(&n))
		return n
	}

	res := h.doSession(t, admin, http.MethodPost, clientsPath, map[string]any{"name": "x", "scopes": []string{"no:such"}})
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	res = h.doSession(t, admin, http.MethodDelete, clientsPath+"/"+uuid.NewString(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	require.Zero(t, count())
}

func (h *harness) createHook(t *testing.T, do func(method, path string, body any) *http.Response, body map[string]any) uuid.UUID {
	t.Helper()
	res := do(http.MethodPost, hooksPath, body)
	b := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
	var out struct{ ID uuid.UUID }
	require.NoError(t, json.Unmarshal(b, &out))
	return out.ID
}

func (h *harness) asSession(t *testing.T) func(method, path string, body any) *http.Response {
	cookie := h.session(t, asAdmin)
	return func(method, path string, body any) *http.Response { return h.doSession(t, cookie, method, path, body) }
}

func TestWebhookAudit_CreateUpdateDelete(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	do := h.asSession(t)

	id := h.createHook(t, do, map[string]any{
		"url": hookURL, "events": []string{"ticket.created", "ticket.replied"}, "secret": hookSigSecret,
	})
	es := h.entriesFor(t, "webhook", id)
	require.Len(t, es, 1)
	require.Equal(t, "webhook_created", es[0].Action)
	require.Equal(t, h.adminID, *es[0].ActorID)
	require.Nil(t, es[0].Before)
	require.Equal(t, map[string]any{
		"url_host": "example.com", "events": "ticket.created,ticket.replied", "enabled": true,
		"payload_format": "raw", "filter": "", "signed": true,
	}, es[0].After)

	patch := func(body map[string]any) {
		t.Helper()
		res := do(http.MethodPatch, hooksPath+"/"+id.String(), body)
		b := readAll(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	}

	t.Run("switching it off is one entry with both sides", func(t *testing.T) {
		patch(map[string]any{"enabled": false})
		es := h.entriesFor(t, "webhook", id)
		require.Len(t, es, 2)
		require.Equal(t, "webhook_updated", es[0].Action)
		require.Equal(t, h.adminID, *es[0].ActorID)
		require.Equal(t, true, es[0].Before["enabled"])
		require.Equal(t, false, es[0].After["enabled"])
		require.NotContains(t, es[0].After, "url_changed", "the URL was not touched")
		require.NotContains(t, es[0].After, "signature_changed")
	})

	t.Run("a patch that changes nothing writes nothing", func(t *testing.T) {
		patch(map[string]any{"enabled": false, "url": hookURL, "secret": hookSigSecret})
		require.Len(t, h.entriesFor(t, "webhook", id), 2)
	})

	t.Run("a new URL on the same host still shows as a change, without showing the URL", func(t *testing.T) {
		patch(map[string]any{"url": "https://example.com/services/OTHER/PATH/ROTATEDTOKEN"})
		es := h.entriesFor(t, "webhook", id)
		require.Len(t, es, 3)
		require.Equal(t, true, es[0].After["url_changed"])
		require.Equal(t, "example.com", es[0].Before["url_host"])
		require.Equal(t, "example.com", es[0].After["url_host"])
	})

	t.Run("a new signing secret is flagged and never stored", func(t *testing.T) {
		patch(map[string]any{"secret": "rotated-signing-secret"})
		es := h.entriesFor(t, "webhook", id)
		require.Len(t, es, 4)
		require.Equal(t, true, es[0].After["signature_changed"])
		require.NotContains(t, h.rawAudit(t, id), "rotated-signing-secret")
	})

	t.Run("events and format changes show as text", func(t *testing.T) {
		patch(map[string]any{"events": []string{"*"}, "payload_format": "slack"})
		es := h.entriesFor(t, "webhook", id)
		require.Len(t, es, 5)
		require.Equal(t, "ticket.created,ticket.replied", es[0].Before["events"])
		require.Equal(t, "*", es[0].After["events"])
		require.Equal(t, "raw", es[0].Before["payload_format"])
		require.Equal(t, "slack", es[0].After["payload_format"])
	})

	t.Run("delete", func(t *testing.T) {
		res := do(http.MethodDelete, hooksPath+"/"+id.String(), nil)
		res.Body.Close()
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		es := h.entriesFor(t, "webhook", id)
		require.Len(t, es, 6)
		require.Equal(t, "webhook_deleted", es[0].Action)
		require.Nil(t, es[0].After)
		require.Equal(t, "example.com", es[0].Before["url_host"])
	})

	raw := h.rawAudit(t, id)
	for _, secret := range []string{hookURLToken, hookURLQuery, hookSigSecret, "ROTATEDTOKEN", "/services/", "https://"} {
		require.NotContains(t, raw, secret, "no part of the URL beyond its host, and no secret, may be stored")
	}
}

func TestWebhookAudit_FilterIsRecordedAsIDsOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	do := h.asSession(t)
	id := h.createHook(t, do, map[string]any{
		"url": "https://example.com/hook", "events": []string{"*"},
		"filter": map[string]any{"category_ids": []string{h.catID.String()}, "priorities": []string{"high"}},
	})
	es := h.entriesFor(t, "webhook", id)
	require.Len(t, es, 1)
	require.JSONEq(t, `{"category_ids":["`+h.catID.String()+`"],"priorities":["high"]}`, es[0].After["filter"].(string))
	require.Equal(t, false, es[0].After["signed"])
}

// A webhook made with an API key is attributed to the key's owner, the same
// actor the key-issue entries and the custom-field entries use for a key.
func TestWebhookAudit_AKeyIsAttributedToItsOwner(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	raw, _ := h.mintAdminKey(t, "webhooks:write")
	do := func(method, path string, body any) *http.Response { return keyDo(t, h, raw, method, path, body) }

	id := h.createHook(t, do, map[string]any{"url": "https://example.com/hook", "events": []string{"*"}})
	es := h.entriesFor(t, "webhook", id)
	require.Len(t, es, 1)
	require.NotNil(t, es[0].ActorID)
	require.Equal(t, h.adminID, *es[0].ActorID)

	res := do(http.MethodDelete, hooksPath+"/"+id.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, "webhook_deleted", h.entriesFor(t, "webhook", id)[0].Action)
}

func TestWebhookAudit_RefusedRequestsWriteNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	do := h.asSession(t)
	count := func() (n int) {
		require.NoError(t, h.tx.QueryRow(`SELECT count(*) FROM audit_log WHERE entity_type = 'webhook'`).Scan(&n))
		return n
	}

	res := do(http.MethodPost, hooksPath, map[string]any{"url": "http://localhost:9200/hook", "events": []string{"*"}})
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	res = do(http.MethodPatch, hooksPath+"/"+uuid.NewString(), map[string]any{"enabled": false})
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	res = do(http.MethodDelete, hooksPath+"/"+uuid.NewString(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	require.Zero(t, count())
}

// What the Audit Log page is sent: the keys survive audit.Redact (the view is an
// allow-list), and nothing in the body is a secret or a piece of the URL.
func TestCredentialAudit_TheAuditViewShowsTheEntries(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	do := h.asSession(t)
	clientID, _, secret := h.createClient(t, "Nightly sync", "tickets:read")
	hookID := h.createHook(t, do, map[string]any{"url": hookURL, "events": []string{"ticket.created"}, "secret": hookSigSecret})

	for _, c := range []struct {
		entityType string
		id         uuid.UUID
		keys       []string
	}{
		{"oauth_client", clientID, []string{"client_name", "scopes", "service_user_id"}},
		{"webhook", hookID, []string{"url_host", "events", "enabled", "payload_format", "filter", "signed"}},
	} {
		res := do(http.MethodGet, "/api/v1/admin/audit?entity_type="+c.entityType, nil)
		body := readAll(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
		var page struct {
			Entries []struct {
				EntityID uuid.UUID      `json:"entity_id"`
				After    map[string]any `json:"after"`
			} `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(body, &page))
		require.Len(t, page.Entries, 1)
		require.Equal(t, c.id, page.Entries[0].EntityID)
		for _, k := range c.keys {
			require.Contains(t, page.Entries[0].After, k, "%s must be on the audit view's allow-list", k)
			require.NotEqual(t, "[redacted]", page.Entries[0].After[k], k)
		}
		for _, s := range []string{secret, hookURLToken, hookURLQuery, hookSigSecret} {
			require.NotContains(t, string(body), s)
		}
	}
}

// Re-approving a hook a key made is the administrator vouching for a
// destination, so it is audited like the edits: same transaction, the hook's
// state on both sides, and key_linked going from true to false. Nothing secret.
func TestWebhookAudit_Reapprove(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	raw, _ := h.mintAdminKey(t, "webhooks:write")
	id := h.createHook(t, func(m, p string, b any) *http.Response { return keyDo(t, h, raw, m, p, b) },
		map[string]any{"url": hookURL, "events": []string{"ticket.created"}, "secret": hookSigSecret})
	admin := h.session(t, asAdmin)

	res := h.doSession(t, admin, http.MethodPost, hooksPath+"/"+id.String()+"/reapprove", nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	es := h.entriesFor(t, "webhook", id)
	require.Len(t, es, 2)
	require.Equal(t, "webhook_reapproved", es[0].Action)
	require.NotNil(t, es[0].ActorID)
	require.Equal(t, h.adminID, *es[0].ActorID, "the signed-in administrator who vouched for it")
	require.Equal(t, true, es[0].Before["key_linked"])
	require.Equal(t, false, es[0].After["key_linked"])
	require.Equal(t, "example.com", es[0].After["url_host"])
	require.Equal(t, true, es[0].After["signed"])
	stored := h.rawAudit(t, id)
	for _, s := range []string{hookURLToken, hookURLQuery, hookSigSecret} {
		require.NotContains(t, stored, s)
	}

	t.Run("the audit view shows key_linked", func(t *testing.T) {
		res := h.doSession(t, admin, http.MethodGet, "/api/v1/admin/audit?entity_type=webhook&action=webhook_reapproved", nil)
		body := readAll(t, res)
		require.Contains(t, string(body), `"key_linked":false`)
	})

	t.Run("a session-made hook can be re-approved too, and says it was never linked", func(t *testing.T) {
		own := h.createHook(t, h.asSession(t), map[string]any{"url": "https://example.com/hook", "events": []string{"*"}})
		res := h.doSession(t, admin, http.MethodPost, hooksPath+"/"+own.String()+"/reapprove", nil)
		res.Body.Close()
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		e := h.entriesFor(t, "webhook", own)[0]
		require.Equal(t, "webhook_reapproved", e.Action)
		require.Equal(t, false, e.Before["key_linked"])
	})

	t.Run("an unknown hook writes nothing", func(t *testing.T) {
		missing := uuid.New()
		res := h.doSession(t, admin, http.MethodPost, hooksPath+"/"+missing.String()+"/reapprove", nil)
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode)
		require.Empty(t, h.entriesFor(t, "webhook", missing))
	})
}
