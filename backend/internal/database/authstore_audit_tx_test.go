package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// An OAuth client or webhook change and its audit entry are one transaction. The
// entry fails here the way customfieldstore's does in TestCustomFieldAudit_
// RolledBackWithTheChange: an actor that is no user, which audit_log's foreign
// key refuses. Needs a database that really commits, which the rolled-back
// harness is not (testutil.NewIsolatedDB).

type credFixture struct {
	t     *testing.T
	db    *testutil.DB
	store *authstore.Store
	admin uuid.UUID // a real user: an entry naming it is accepted
	ghost uuid.UUID // no such user: an entry naming it is refused
}

func newCredFixture(t *testing.T) *credFixture {
	t.Helper()
	db, closeDB := testutil.NewIsolatedDB(t)
	t.Cleanup(closeDB)
	f := &credFixture{t: t, db: db, store: authstore.NewWithDB(db.Queries, db.SQL), admin: uuid.New(), ghost: uuid.New()}
	_, err := db.SQL.Exec(`INSERT INTO users (id, email, display_name, role) VALUES ($1, 'admin@cred.test', 'Admin', 'admin')`, f.admin)
	require.NoError(t, err)
	return f
}

func (f *credFixture) count(q string, args ...any) (n int) {
	f.t.Helper()
	require.NoError(f.t, f.db.SQL.QueryRow(q, args...).Scan(&n))
	return n
}

func (f *credFixture) entry(actor uuid.UUID, entityType string, id uuid.UUID, action string) audit.Entry {
	return audit.Entry{ID: uuid.New(), ActorID: &actor, EntityType: entityType, EntityID: id, Action: action, CreatedAt: time.Now()}
}

func (f *credFixture) client() auth.OAuthClient {
	id := uuid.New()
	return auth.OAuthClient{ID: id, ClientID: id.String()[:16], HashedSecret: "h", Name: "CI", Scopes: []string{"tickets:read"},
		CreatedAt: time.Now(), UserID: uuid.New()}
}

func (f *credFixture) hook() authstore.WebhookConfig {
	return authstore.WebhookConfig{ID: uuid.New(), URL: "https://example.com/hook", Events: []string{"ticket.created"},
		Enabled: true, CreatedAt: time.Now(), PayloadFormat: "raw"}
}

func TestOAuthClientAudit_CommitsWithTheChange(t *testing.T) {
	f := newCredFixture(t)
	ctx := context.Background()
	c := f.client()

	require.NoError(t, f.store.CreateOAuthClientAudited(ctx, c, f.entry(f.admin, "oauth_client", c.ID, "oauth_client_created")))
	require.Equal(t, 1, f.count(`SELECT count(*) FROM oauth_clients WHERE id = $1`, c.ID))
	require.Equal(t, 1, f.count(`SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = 'oauth_client_created'`, c.ID))

	var seen auth.OAuthClient
	require.NoError(t, f.store.DeleteOAuthClientAudited(ctx, c.ID, func(before auth.OAuthClient) audit.Entry {
		seen = before
		return f.entry(f.admin, "oauth_client", c.ID, "oauth_client_deleted")
	}))
	require.Equal(t, c.Name, seen.Name, "the entry is built from the row as it was in the transaction")
	require.Equal(t, c.UserID, seen.UserID)
	require.Zero(t, f.count(`SELECT count(*) FROM oauth_clients WHERE id = $1`, c.ID))
	require.Equal(t, 1, f.count(`SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = 'oauth_client_deleted'`, c.ID))
}

func TestOAuthClientAudit_RolledBackWithTheChange(t *testing.T) {
	f := newCredFixture(t)
	ctx := context.Background()

	t.Run("create", func(t *testing.T) {
		c := f.client()
		require.Error(t, f.store.CreateOAuthClientAudited(ctx, c, f.entry(f.ghost, "oauth_client", c.ID, "oauth_client_created")))
		require.Zero(t, f.count(`SELECT count(*) FROM oauth_clients WHERE id = $1`, c.ID), "the client must not exist without its entry")
		require.Zero(t, f.count(`SELECT count(*) FROM users WHERE id = $1`, c.UserID), "nor its service user")
	})
	t.Run("delete", func(t *testing.T) {
		c := f.client()
		require.NoError(t, f.store.CreateOAuthClientAudited(ctx, c, f.entry(f.admin, "oauth_client", c.ID, "oauth_client_created")))
		err := f.store.DeleteOAuthClientAudited(ctx, c.ID, func(auth.OAuthClient) audit.Entry {
			return f.entry(f.ghost, "oauth_client", c.ID, "oauth_client_deleted")
		})
		require.Error(t, err)
		require.Equal(t, 1, f.count(`SELECT count(*) FROM oauth_clients WHERE id = $1`, c.ID), "the client must survive a delete whose entry failed")
		require.Zero(t, f.count(`SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NOT NULL`, c.UserID), "and its service user must not be soft-deleted")
	})
	t.Run("deleting a client that is not there is a not-found and writes nothing", func(t *testing.T) {
		missing := uuid.New()
		err := f.store.DeleteOAuthClientAudited(ctx, missing, func(auth.OAuthClient) audit.Entry {
			return f.entry(f.admin, "oauth_client", missing, "oauth_client_deleted")
		})
		require.ErrorIs(t, err, auth.ErrNotFound)
		require.Zero(t, f.count(`SELECT count(*) FROM audit_log WHERE entity_id = $1`, missing))
	})
}

// Rotating a secret and its entry are one transaction too (#622): a rotation
// whose entry failed must leave the old secret working.
func TestOAuthClientSecretRotation_AuditedInOneTransaction(t *testing.T) {
	f := newCredFixture(t)
	ctx := context.Background()
	c := f.client()
	require.NoError(t, f.store.CreateOAuthClientAudited(ctx, c, f.entry(f.admin, "oauth_client", c.ID, "oauth_client_created")))
	hash := func() (h string) {
		require.NoError(t, f.db.SQL.QueryRow(`SELECT hashed_secret FROM oauth_clients WHERE id = $1`, c.ID).Scan(&h))
		return h
	}

	t.Run("a failed entry keeps the old secret", func(t *testing.T) {
		err := f.store.RotateOAuthClientSecretAudited(ctx, c.ID, "new-hash", func(auth.OAuthClient) audit.Entry {
			return f.entry(f.ghost, "oauth_client", c.ID, "oauth_client_secret_rotated")
		})
		require.Error(t, err)
		require.Equal(t, "h", hash())
		require.Zero(t, f.count(`SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = 'oauth_client_secret_rotated'`, c.ID))
	})
	t.Run("commits with its entry, built from the row as it was", func(t *testing.T) {
		var seen auth.OAuthClient
		require.NoError(t, f.store.RotateOAuthClientSecretAudited(ctx, c.ID, "new-hash", func(before auth.OAuthClient) audit.Entry {
			seen = before
			return f.entry(f.admin, "oauth_client", c.ID, "oauth_client_secret_rotated")
		}))
		require.Equal(t, c.Name, seen.Name)
		require.Equal(t, "new-hash", hash())
		require.Equal(t, 1, f.count(`SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = 'oauth_client_secret_rotated'`, c.ID))
	})
	t.Run("a client that is not there is a not-found and writes nothing", func(t *testing.T) {
		missing := uuid.New()
		err := f.store.RotateOAuthClientSecretAudited(ctx, missing, "x", func(auth.OAuthClient) audit.Entry {
			return f.entry(f.admin, "oauth_client", missing, "oauth_client_secret_rotated")
		})
		require.ErrorIs(t, err, auth.ErrNotFound)
		require.Zero(t, f.count(`SELECT count(*) FROM audit_log WHERE entity_id = $1`, missing))
	})
}

func TestWebhookAudit_CommitsWithTheChange(t *testing.T) {
	f := newCredFixture(t)
	ctx := context.Background()
	wh := f.hook()

	require.NoError(t, f.store.CreateWebhookAudited(ctx, wh, f.entry(f.admin, "webhook", wh.ID, "webhook_created")))

	next := wh
	next.Enabled = false
	var seen authstore.WebhookConfig
	require.NoError(t, f.store.UpdateWebhookAudited(ctx, next, func(before authstore.WebhookConfig) *audit.Entry {
		seen = before
		e := f.entry(f.admin, "webhook", wh.ID, "webhook_updated")
		return &e
	}))
	require.True(t, seen.Enabled, "the entry is built from the row as it was in the transaction")
	got, err := f.store.GetWebhook(ctx, wh.ID)
	require.NoError(t, err)
	require.False(t, got.Enabled)

	require.NoError(t, f.store.UpdateWebhookAudited(ctx, next, func(authstore.WebhookConfig) *audit.Entry { return nil }))
	require.Equal(t, 1, f.count(`SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = 'webhook_updated'`, wh.ID), "a nil entry writes none")

	require.NoError(t, f.store.DeleteWebhookAudited(ctx, wh.ID, func(authstore.WebhookConfig) audit.Entry {
		return f.entry(f.admin, "webhook", wh.ID, "webhook_deleted")
	}))
	require.Zero(t, f.count(`SELECT count(*) FROM webhook_configs WHERE id = $1`, wh.ID))
	require.Equal(t, 3, f.count(`SELECT count(*) FROM audit_log WHERE entity_id = $1`, wh.ID))
}

func TestWebhookAudit_RolledBackWithTheChange(t *testing.T) {
	f := newCredFixture(t)
	ctx := context.Background()

	t.Run("create", func(t *testing.T) {
		wh := f.hook()
		require.Error(t, f.store.CreateWebhookAudited(ctx, wh, f.entry(f.ghost, "webhook", wh.ID, "webhook_created")))
		require.Zero(t, f.count(`SELECT count(*) FROM webhook_configs WHERE id = $1`, wh.ID))
	})

	wh := f.hook()
	require.NoError(t, f.store.CreateWebhookAudited(ctx, wh, f.entry(f.admin, "webhook", wh.ID, "webhook_created")))
	require.NoError(t, f.store.RecordWebhookAttempt(ctx, authstore.WebhookAttempt{DeliveryID: uuid.New(), HookID: wh.ID, EventType: "ticket.created", Event: []byte(`{}`),
		Result: authstore.WebhookDelivery{At: time.Now(), Status: 200}}))

	t.Run("update, including the history a new URL clears", func(t *testing.T) {
		next := wh
		next.URL = "https://example.com/elsewhere"
		next.Enabled = false
		e := f.entry(f.ghost, "webhook", wh.ID, "webhook_updated")
		require.Error(t, f.store.UpdateWebhookAudited(ctx, next, func(authstore.WebhookConfig) *audit.Entry { return &e }))
		got, err := f.store.GetWebhook(ctx, wh.ID)
		require.NoError(t, err)
		require.Equal(t, wh.URL, got.URL, "the hook must not change without its entry")
		require.True(t, got.Enabled)
		require.Equal(t, 1, f.count(`SELECT count(*) FROM webhook_deliveries WHERE webhook_id = $1`, wh.ID), "nor lose its history")
	})
	t.Run("delete", func(t *testing.T) {
		err := f.store.DeleteWebhookAudited(ctx, wh.ID, func(authstore.WebhookConfig) audit.Entry {
			return f.entry(f.ghost, "webhook", wh.ID, "webhook_deleted")
		})
		require.Error(t, err)
		require.Equal(t, 1, f.count(`SELECT count(*) FROM webhook_configs WHERE id = $1`, wh.ID))
	})
	t.Run("updating or deleting a hook that is not there is a not-found", func(t *testing.T) {
		gone := f.hook()
		require.ErrorIs(t, f.store.UpdateWebhookAudited(ctx, gone, func(authstore.WebhookConfig) *audit.Entry { return nil }), auth.ErrNotFound)
		require.ErrorIs(t, f.store.DeleteWebhookAudited(ctx, gone.ID, func(authstore.WebhookConfig) audit.Entry {
			return f.entry(f.admin, "webhook", gone.ID, "webhook_deleted")
		}), auth.ErrNotFound)
	})
}

func TestWebhookReapproveAudit_CommitsAndRollsBackWithTheChange(t *testing.T) {
	f := newCredFixture(t)
	ctx := context.Background()
	var keyID uuid.UUID
	require.NoError(t, f.db.SQL.QueryRow(`INSERT INTO api_keys (name, hashed_token, user_id, scopes) VALUES ('k', 'h', $1, '{webhooks:write}') RETURNING id`, f.admin).Scan(&keyID))
	wh := f.hook()
	wh.APIKeyID = &keyID
	require.NoError(t, f.store.CreateWebhookAudited(ctx, wh, f.entry(f.admin, "webhook", wh.ID, "webhook_created")))
	linked := func() int {
		return f.count(`SELECT count(*) FROM webhook_configs WHERE id = $1 AND api_key_id IS NOT NULL`, wh.ID)
	}

	err := f.store.ReapproveWebhookAudited(ctx, wh.ID, func(authstore.WebhookConfig) audit.Entry {
		return f.entry(f.ghost, "webhook", wh.ID, "webhook_reapproved")
	})
	require.Error(t, err)
	require.Equal(t, 1, linked(), "the hook must stay tied to its key when its entry failed")

	var seen authstore.WebhookConfig
	require.NoError(t, f.store.ReapproveWebhookAudited(ctx, wh.ID, func(before authstore.WebhookConfig) audit.Entry {
		seen = before
		return f.entry(f.admin, "webhook", wh.ID, "webhook_reapproved")
	}))
	require.NotNil(t, seen.APIKeyID, "the entry is built from the hook as it was")
	require.Zero(t, linked())
	require.Equal(t, 1, f.count(`SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = 'webhook_reapproved'`, wh.ID))

	gone := uuid.New()
	require.ErrorIs(t, f.store.ReapproveWebhookAudited(ctx, gone, func(authstore.WebhookConfig) audit.Entry {
		return f.entry(f.admin, "webhook", gone, "webhook_reapproved")
	}), auth.ErrNotFound)
	require.Zero(t, f.count(`SELECT count(*) FROM audit_log WHERE entity_id = $1`, gone))
}
