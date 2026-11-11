package server

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// What the audit entries for OAuth clients and webhooks say. The store writes
// them in the same transaction as the change (authstore's *Audited methods), so
// an administrator cannot change a credential or a hook and leave no trace; the
// API key entries (auditAPIKey) are the best-effort kind and these are not.
//
// The entries follow the key ones: an entity type per kind of thing, an action
// per change, the signed-in administrator as the actor (for an API key, its
// owner), and a before/after the audit view can show (audit.shownKeys is the
// allow-list, and every key used here is on it).
//
// Nothing secret goes in. A client's secret is shown once and stored only as a
// hash, and neither is written. A webhook's URL is not written either: its path
// and query are where Slack, Discord and Teams put the token that lets anyone
// post to the channel, so the entry carries only the host, and url_changed says
// the URL was replaced (a rotated token on the same host would otherwise show
// as no change at all). The signing secret is recorded as the bare fact that
// one is set (signed) and that a change replaced it (signature_changed).
const (
	auditEntityOAuthClient = "oauth_client"
	auditEntityWebhook     = "webhook"

	auditActionOAuthClientCreated = "oauth_client_created"
	auditActionOAuthClientDeleted = "oauth_client_deleted"
	auditActionWebhookCreated     = "webhook_created"
	auditActionWebhookUpdated     = "webhook_updated"
	auditActionWebhookDeleted     = "webhook_deleted"
	auditActionWebhookReapproved  = "webhook_reapproved"
)

func credentialEntry(actor uuid.UUID, entityType string, entityID uuid.UUID, action string, before, after map[string]any) audit.Entry {
	return audit.Entry{
		ID: uuid.New(), ActorID: &actor, EntityType: entityType, EntityID: entityID,
		Action: action, Before: before, After: after, CreatedAt: time.Now(),
	}
}

// oauthClientState is what a client's entries say about it. The name is the
// text the administrator typed, like a custom field's name, and is what tells
// two clients apart once one is deleted.
func oauthClientState(c auth.OAuthClient) map[string]any {
	return map[string]any{
		"client_name":     c.Name,
		"scopes":          strings.Join(c.Scopes, ","),
		"service_user_id": c.UserID.String(),
	}
}

func oauthClientCreatedEntry(actor uuid.UUID, c auth.OAuthClient) audit.Entry {
	return credentialEntry(actor, auditEntityOAuthClient, c.ID, auditActionOAuthClientCreated, nil, oauthClientState(c))
}

func oauthClientDeletedEntry(actor uuid.UUID, c auth.OAuthClient) audit.Entry {
	return credentialEntry(actor, auditEntityOAuthClient, c.ID, auditActionOAuthClientDeleted, oauthClientState(c), nil)
}

// webhookState is what a webhook's entries say about it. The filter is one JSON
// string, because the audit view drops arrays; it holds ids and priorities only
// and is "" for no filter, so "had none" and "has one" are different values.
func webhookState(wh authstore.WebhookConfig) map[string]any {
	return map[string]any{
		"url_host":       hostOf(wh.URL),
		"events":         strings.Join(wh.Events, ","),
		"enabled":        wh.Enabled,
		"payload_format": wh.PayloadFormat,
		"filter":         filterText(wh.Filter),
		"signed":         wh.Secret != "",
	}
}

// hostOf is the host of a webhook URL, with no port, path, query or userinfo:
// the one part of it that is safe to keep. A URL that does not parse has no
// host to give, and the entry says "" and not the URL.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func filterText(f notification.WebhookFilter) string {
	if f.IsEmpty() && !f.Invert {
		return ""
	}
	// omitempty so a filter with only priorities reads as just that.
	b, err := json.Marshal(struct {
		CategoryIDs []uuid.UUID `json:"category_ids,omitempty"`
		Priorities  []string    `json:"priorities,omitempty"`
		GroupIDs    []uuid.UUID `json:"group_ids,omitempty"`
		Invert      bool        `json:"invert,omitempty"`
	}{f.CategoryIDs, f.Priorities, f.GroupIDs, f.Invert})
	if err != nil {
		return "" // unreachable for these types; not worth failing a change over
	}
	return string(b)
}

func webhookCreatedEntry(actor uuid.UUID, wh authstore.WebhookConfig) audit.Entry {
	return credentialEntry(actor, auditEntityWebhook, wh.ID, auditActionWebhookCreated, nil, webhookState(wh))
}

func webhookDeletedEntry(actor uuid.UUID, wh authstore.WebhookConfig) audit.Entry {
	return credentialEntry(actor, auditEntityWebhook, wh.ID, auditActionWebhookDeleted, webhookState(wh), nil)
}

// webhookUpdatedEntry compares the hook as it was with the hook as saved, and
// answers nil when nothing an entry can say has changed (a PATCH that repeats
// what is stored). A change to the URL beyond its host, or to the secret, is
// invisible in webhookState by design, so the two flags carry it.
func webhookUpdatedEntry(actor uuid.UUID, before, after authstore.WebhookConfig) *audit.Entry {
	b, a := webhookState(before), webhookState(after)
	urlChanged := before.URL != after.URL
	sigChanged := before.Secret != after.Secret
	if !urlChanged && !sigChanged && mapsEqual(b, a) {
		return nil
	}
	if urlChanged {
		a["url_changed"] = true
	}
	if sigChanged {
		a["signature_changed"] = true
	}
	e := credentialEntry(actor, auditEntityWebhook, after.ID, auditActionWebhookUpdated, b, a)
	return &e
}

// webhookReapprovedEntry records a signed-in administrator vouching for a hook
// a key made or re-pointed (POST /admin/webhooks/{id}/reapprove). Nothing in the
// hook changes except its link to that key, so key_linked is the one key that
// differs: true before when a key was tied to it, false after. A session-made
// hook has no link and reads false on both sides; the entry is still written,
// because the approval itself is what is being recorded.
func webhookReapprovedEntry(actor uuid.UUID, before authstore.WebhookConfig) audit.Entry {
	b, a := webhookState(before), webhookState(before)
	b["key_linked"] = before.APIKeyID != nil
	a["key_linked"] = false
	return credentialEntry(actor, auditEntityWebhook, before.ID, auditActionWebhookReapproved, b, a)
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || v != w {
			return false
		}
	}
	return true
}
