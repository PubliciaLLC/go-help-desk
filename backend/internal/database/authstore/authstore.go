// Package authstore implements auth.APIKeyStore and auth.OAuthClientStore.
package authstore

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/database"
	"github.com/open-help-desk/open-help-desk/backend/internal/dbgen"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/auth"
)

// Store implements both auth.APIKeyStore and auth.OAuthClientStore.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

// ── APIKeyStore ──────────────────────────────────────────────────────────────

func (s *Store) CreateAPIKey(ctx context.Context, k auth.APIKey) error {
	return s.q.CreateAPIKey(ctx, dbgen.CreateAPIKeyParams{
		ID:          k.ID,
		Name:        k.Name,
		HashedToken: k.HashedToken,
		UserID:      k.UserID,
		Scopes:      k.Scopes,
		ExpiresAt:   database.NullTime(k.ExpiresAt),
		CreatedAt:   k.CreatedAt,
	})
}

func (s *Store) GetByHash(ctx context.Context, hashed string) (auth.APIKey, error) {
	r, err := s.q.GetAPIKeyByHash(ctx, hashed)
	if err != nil {
		return auth.APIKey{}, fmt.Errorf("getting API key: %w", err)
	}
	return auth.APIKey{
		ID:          r.ID,
		Name:        r.Name,
		HashedToken: r.HashedToken,
		UserID:      r.UserID,
		Scopes:      r.Scopes,
		LastUsedAt:  database.TimePtr(r.LastUsedAt),
		ExpiresAt:   database.TimePtr(r.ExpiresAt),
		CreatedAt:   r.CreatedAt,
	}, nil
}

func (s *Store) UpdateLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	return s.q.UpdateAPIKeyLastUsed(ctx, dbgen.UpdateAPIKeyLastUsedParams{
		ID:         id,
		LastUsedAt: database.NullTime(&at),
	})
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteAPIKey(ctx, id)
}

func (s *Store) ListByUser(ctx context.Context, userID uuid.UUID) ([]auth.APIKey, error) {
	rows, err := s.q.ListAPIKeysByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing API keys: %w", err)
	}
	out := make([]auth.APIKey, len(rows))
	for i, r := range rows {
		out[i] = auth.APIKey{
			ID:          r.ID,
			Name:        r.Name,
			HashedToken: r.HashedToken,
			UserID:      r.UserID,
			Scopes:      r.Scopes,
			LastUsedAt:  database.TimePtr(r.LastUsedAt),
			ExpiresAt:   database.TimePtr(r.ExpiresAt),
			CreatedAt:   r.CreatedAt,
		}
	}
	return out, nil
}

// ── OAuthClientStore ─────────────────────────────────────────────────────────

func (s *Store) CreateOAuthClient(ctx context.Context, c auth.OAuthClient) error {
	return s.q.CreateOAuthClient(ctx, dbgen.CreateOAuthClientParams{
		ID:           c.ID,
		ClientID:     c.ClientID,
		HashedSecret: c.HashedSecret,
		Name:         c.Name,
		Scopes:       c.Scopes,
		CreatedAt:    c.CreatedAt,
	})
}

func (s *Store) GetByClientID(ctx context.Context, clientID string) (auth.OAuthClient, error) {
	r, err := s.q.GetOAuthClientByClientID(ctx, clientID)
	if err != nil {
		return auth.OAuthClient{}, fmt.Errorf("getting OAuth client %q: %w", clientID, err)
	}
	return auth.OAuthClient{
		ID:           r.ID,
		ClientID:     r.ClientID,
		HashedSecret: r.HashedSecret,
		Name:         r.Name,
		Scopes:       r.Scopes,
		CreatedAt:    r.CreatedAt,
	}, nil
}

func (s *Store) DeleteOAuthClient(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteOAuthClient(ctx, id)
}

func (s *Store) ListOAuthClients(ctx context.Context) ([]auth.OAuthClient, error) {
	rows, err := s.q.ListOAuthClients(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing OAuth clients: %w", err)
	}
	out := make([]auth.OAuthClient, len(rows))
	for i, r := range rows {
		out[i] = auth.OAuthClient{
			ID:           r.ID,
			ClientID:     r.ClientID,
			HashedSecret: r.HashedSecret,
			Name:         r.Name,
			Scopes:       r.Scopes,
			CreatedAt:    r.CreatedAt,
		}
	}
	return out, nil
}

// ── WebhookConfigStore ───────────────────────────────────────────────────────

// WebhookConfig is the persisted record for an outbound webhook.
type WebhookConfig struct {
	ID        uuid.UUID `json:"id"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Secret    string    `json:"-"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CreateWebhook(ctx context.Context, wh WebhookConfig) error {
	return s.q.CreateWebhookConfig(ctx, dbgen.CreateWebhookConfigParams{
		ID:        wh.ID,
		Url:       wh.URL,
		Events:    wh.Events,
		Secret:    wh.Secret,
		Enabled:   wh.Enabled,
		CreatedAt: wh.CreatedAt,
	})
}

func (s *Store) GetWebhook(ctx context.Context, id uuid.UUID) (WebhookConfig, error) {
	r, err := s.q.GetWebhookConfig(ctx, id)
	if err != nil {
		return WebhookConfig{}, fmt.Errorf("getting webhook %s: %w", id, err)
	}
	return WebhookConfig{ID: r.ID, URL: r.Url, Events: r.Events, Secret: r.Secret, Enabled: r.Enabled, CreatedAt: r.CreatedAt}, nil
}

func (s *Store) UpdateWebhook(ctx context.Context, wh WebhookConfig) error {
	return s.q.UpdateWebhookConfig(ctx, dbgen.UpdateWebhookConfigParams{
		ID:      wh.ID,
		Url:     wh.URL,
		Events:  wh.Events,
		Secret:  wh.Secret,
		Enabled: wh.Enabled,
	})
}

func (s *Store) DeleteWebhook(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteWebhookConfig(ctx, id)
}

func (s *Store) ListEnabledWebhooks(ctx context.Context) ([]WebhookConfig, error) {
	rows, err := s.q.ListEnabledWebhookConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing enabled webhooks: %w", err)
	}
	out := make([]WebhookConfig, len(rows))
	for i, r := range rows {
		out[i] = WebhookConfig{ID: r.ID, URL: r.Url, Events: r.Events, Secret: r.Secret, Enabled: r.Enabled, CreatedAt: r.CreatedAt}
	}
	return out, nil
}
