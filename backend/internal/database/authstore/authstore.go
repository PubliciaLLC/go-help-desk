// Package authstore implements auth.APIKeyStore and auth.OAuthClientStore.
package authstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Store implements both auth.APIKeyStore and auth.OAuthClientStore.
type Store struct {
	q *dbgen.Queries
	// now is the application clock, the one APIKeyAuth judges expiry by.
	now func() time.Time
}

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q, now: time.Now} }

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
		CreatedBy:   database.NullUUID(k.CreatedBy),
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
		CreatedBy:   database.UUIDPtr(r.CreatedBy),
	}, nil
}

func (s *Store) UpdateLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	return s.q.UpdateAPIKeyLastUsed(ctx, dbgen.UpdateAPIKeyLastUsedParams{
		ID:         id,
		LastUsedAt: database.NullTime(&at),
	})
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.DeleteAPIKey(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting API key %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("API key %s: %w", id, auth.ErrNotFound)
	}
	return nil
}

func (s *Store) DeleteForUser(ctx context.Context, id, userID uuid.UUID) error {
	n, err := s.q.DeleteAPIKeyForUser(ctx, dbgen.DeleteAPIKeyForUserParams{ID: id, UserID: userID})
	if err != nil {
		return fmt.Errorf("deleting API key %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("API key %s: %w", id, auth.ErrNotFound)
	}
	return nil
}

// DeleteAllForUser removes every key the user owns and says how many there were.
func (s *Store) DeleteAllForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	n, err := s.q.DeleteAllAPIKeysForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("deleting all API keys of %s: %w", userID, err)
	}
	return n, nil
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
			CreatedBy:   database.UUIDPtr(r.CreatedBy),
		}
	}
	return out, nil
}

// ListWithOwner lists every key when keyID is nil, or just that key, newest
// first, each with its owner's address and name.
func (s *Store) ListWithOwner(ctx context.Context, keyID *uuid.UUID) ([]auth.APIKeyWithOwner, error) {
	rows, err := s.q.ListAPIKeysWithOwner(ctx, database.NullUUID(keyID))
	if err != nil {
		return nil, fmt.Errorf("listing API keys with owners: %w", err)
	}
	out := make([]auth.APIKeyWithOwner, len(rows))
	for i, r := range rows {
		out[i] = auth.APIKeyWithOwner{
			APIKey: auth.APIKey{
				ID:          r.ID,
				Name:        r.Name,
				HashedToken: r.HashedToken,
				UserID:      r.UserID,
				Scopes:      r.Scopes,
				LastUsedAt:  database.TimePtr(r.LastUsedAt),
				ExpiresAt:   database.TimePtr(r.ExpiresAt),
				CreatedAt:   r.CreatedAt,
				CreatedBy:   database.UUIDPtr(r.CreatedBy),
			},
			OwnerEmail: r.OwnerEmail,
			OwnerName:  r.OwnerName,
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
		UserID:       c.UserID,
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
		UserID:       r.UserID,
	}, nil
}

// OAuthClientNameForUser returns the name of the client a service user belongs
// to, or auth.ErrNotFound when no client holds it.
func (s *Store) OAuthClientNameForUser(ctx context.Context, userID uuid.UUID) (string, error) {
	name, err := s.q.GetOAuthClientNameByUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no OAuth client for user %s: %w", userID, auth.ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("getting OAuth client for user %s: %w", userID, err)
	}
	return name, nil
}

// ListOAuthClientGroups returns the groups of every client's service user,
// keyed by the client row's id, each list ordered by group name.
func (s *Store) ListOAuthClientGroups(ctx context.Context) (map[uuid.UUID][]auth.ClientGroup, error) {
	rows, err := s.q.ListOAuthClientGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing OAuth client groups: %w", err)
	}
	out := make(map[uuid.UUID][]auth.ClientGroup)
	for _, r := range rows {
		out[r.ClientRowID] = append(out[r.ClientRowID], auth.ClientGroup{ID: r.GroupID, Name: r.GroupName})
	}
	return out, nil
}

// ListOAuthClientsWithoutGroups names the clients that are switched on and
// whose service user is in no group.
func (s *Store) ListOAuthClientsWithoutGroups(ctx context.Context) ([]string, error) {
	names, err := s.q.ListOAuthClientsWithoutGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing OAuth clients without groups: %w", err)
	}
	return names, nil
}

// GetWithUser returns the client and the service user it acts as, in one query:
// it runs on every OAuth-authenticated request. A deleted client has no row, so
// its outstanding tokens stop working; the user's own state (disabled, deleted)
// is returned for the caller to judge.
func (s *Store) GetWithUser(ctx context.Context, clientID string) (auth.OAuthClient, user.User, error) {
	r, err := s.q.GetOAuthClientWithUser(ctx, clientID)
	if err != nil {
		return auth.OAuthClient{}, user.User{}, fmt.Errorf("getting OAuth client %q: %w", clientID, err)
	}
	c := r.OauthClient
	return auth.OAuthClient{
		ID:           c.ID,
		ClientID:     c.ClientID,
		HashedSecret: c.HashedSecret,
		Name:         c.Name,
		Scopes:       c.Scopes,
		CreatedAt:    c.CreatedAt,
		UserID:       c.UserID,
	}, userstore.FromRow(r.User), nil
}

func (s *Store) DeleteOAuthClient(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.DeleteOAuthClient(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting OAuth client %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("OAuth client %s: %w", id, auth.ErrNotFound)
	}
	return nil
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
			UserID:       r.UserID,
		}
	}
	return out, nil
}

// ── WebhookConfigStore ───────────────────────────────────────────────────────

// WebhookDelivery is the result of the latest delivery attempt to a hook.
// Deliberately no message field: see migration 000034.
type WebhookDelivery struct {
	At     time.Time `json:"at"`     // when the attempt started
	Status int       `json:"status"` // HTTP status; 0 when no response came back, or one outside 100..999 did (Error tells which)
	Error  string    `json:"error"`  // "" on success, else one of notify.DeliveryErrors
}

// WebhookConfig is the persisted record for an outbound webhook.
type WebhookConfig struct {
	ID        uuid.UUID `json:"id"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Secret    string    `json:"-"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`

	// PayloadFormat is one of notify.Formats: "raw" (default; today's full
	// event payload) or a chat/ITSM shape ("slack", "teams", "discord",
	// "jira"). Not typed as notify.Format here — that would make this package
	// import notify, and the dependency runs the other way (notify's
	// WebhookDispatcher reads this store).
	PayloadFormat string `json:"payload_format"`

	// LastDelivery is nil until a delivery has been attempted since the hook
	// was created or its URL last changed.
	LastDelivery *WebhookDelivery `json:"last_delivery"`

	// APIKeyID is the API key that created the hook or last re-pointed its URL
	// (#450); nil for a hook made or last re-pointed in a session. A hook tied
	// to a key that has been deleted or has expired is Paused.
	APIKeyID *uuid.UUID `json:"-"`

	// Paused and PausedReason are worked out when the hook is read, never
	// stored: a hook whose key died is paused without any job having run.
	Paused       bool   `json:"paused"`
	PausedReason string `json:"paused_reason,omitempty"`

	// Filter narrows which tickets the hook fires for (#413). The zero value
	// is no filter, which is what every hook created before #413 has.
	Filter notification.WebhookFilter `json:"filter"`
}

// defaultPayloadFormat is "raw": the column is NOT NULL with no useful
// zero value, and the Go zero value for WebhookConfig.PayloadFormat is "" —
// which the CHECK constraint rejects outright. Defaulting it here, not only
// in the admin handler, means any caller that predates this field (existing
// tests included) keeps working without having to learn about it.
const defaultPayloadFormat = "raw"

// webhookFromRow decodes a row. The filter column is a CHECK-constrained
// object, so an error here means a row written outside this application.
func webhookFromRow(r dbgen.WebhookConfig) (WebhookConfig, error) {
	var filter notification.WebhookFilter
	if err := json.Unmarshal(r.TicketFilter, &filter); err != nil {
		return WebhookConfig{}, fmt.Errorf("decoding filter of webhook %s: %w", r.ID, err)
	}
	wh := WebhookConfig{ID: r.ID, URL: r.Url, Events: r.Events, Secret: r.Secret,
		Enabled: r.Enabled, CreatedAt: r.CreatedAt, PayloadFormat: r.PayloadFormat,
		Filter: filter, APIKeyID: database.UUIDPtr(r.ApiKeyID)}
	if r.LastDeliveryAt.Valid {
		wh.LastDelivery = &WebhookDelivery{At: r.LastDeliveryAt.Time.UTC(),
			Status: int(r.LastDeliveryStatus), Error: r.LastDeliveryError}
	}
	return wh, nil
}

// encodeFilter is the stored form of a filter: a JSON object, never null.
func encodeFilter(f notification.WebhookFilter) ([]byte, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("encoding ticket filter: %w", err)
	}
	return b, nil
}

func (s *Store) CreateWebhook(ctx context.Context, wh WebhookConfig) error {
	format := wh.PayloadFormat
	if format == "" {
		format = defaultPayloadFormat
	}
	filter, err := encodeFilter(wh.Filter)
	if err != nil {
		return err
	}
	return s.q.CreateWebhookConfig(ctx, dbgen.CreateWebhookConfigParams{
		ID:            wh.ID,
		Url:           wh.URL,
		Events:        wh.Events,
		Secret:        wh.Secret,
		Enabled:       wh.Enabled,
		CreatedAt:     wh.CreatedAt,
		PayloadFormat: format,
		TicketFilter:  filter,
		ApiKeyID:      database.NullUUID(wh.APIKeyID),
	})
}

func (s *Store) GetWebhook(ctx context.Context, id uuid.UUID) (WebhookConfig, error) {
	r, err := s.q.GetWebhookConfig(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WebhookConfig{}, fmt.Errorf("webhook %s: %w", id, auth.ErrNotFound)
		}
		return WebhookConfig{}, fmt.Errorf("getting webhook %s: %w", id, err)
	}
	wh, err := webhookFromRow(r)
	if err != nil {
		return WebhookConfig{}, err
	}
	paused, err := s.pausedSet(ctx)
	if err != nil {
		return WebhookConfig{}, err
	}
	markPaused(&wh, paused)
	return wh, nil
}

// PausedReason is what the admin page and the API say about a hook whose key
// stopped working. It covers every way that happens: the key was revoked or
// expired, or its owner was disabled or deleted. Keep it in step with the
// "paused" drop label in WebhooksPage.tsx.
const PausedReason = "Its API key, or that key's owner, is no longer active"

func markPaused(wh *WebhookConfig, paused map[uuid.UUID]bool) {
	if paused[wh.ID] {
		wh.Paused, wh.PausedReason = true, PausedReason
	}
}

// pausedSet is the ids of hooks tied to a key that is gone or expired.
func (s *Store) pausedSet(ctx context.Context) (map[uuid.UUID]bool, error) {
	ids, err := s.q.ListPausedWebhookIDs(ctx, s.now())
	if err != nil {
		return nil, fmt.Errorf("finding paused webhooks: %w", err)
	}
	out := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// ReapproveWebhook clears a hook's link to the API key that made it, so it no
// longer depends on that key. Only a signed-in administrator reaches it.
func (s *Store) ReapproveWebhook(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.ReapproveWebhookConfig(ctx, id)
	if err != nil {
		return fmt.Errorf("re-approving webhook %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("webhook %s: %w", id, auth.ErrNotFound)
	}
	return nil
}

func (s *Store) UpdateWebhook(ctx context.Context, wh WebhookConfig) error {
	format := wh.PayloadFormat
	if format == "" {
		format = defaultPayloadFormat
	}
	filter, err := encodeFilter(wh.Filter)
	if err != nil {
		return err
	}
	// Delivery history describes a target, like the latest result the update
	// query already clears when the URL changes (#448).
	before, err := s.q.GetWebhookConfig(ctx, wh.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("webhook %s: %w", wh.ID, auth.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("getting webhook %s: %w", wh.ID, err)
	}
	if err := s.q.UpdateWebhookConfig(ctx, dbgen.UpdateWebhookConfigParams{
		ID:            wh.ID,
		Url:           wh.URL,
		Events:        wh.Events,
		Secret:        wh.Secret,
		Enabled:       wh.Enabled,
		PayloadFormat: format,
		TicketFilter:  filter,
		ApiKeyID:      database.NullUUID(wh.APIKeyID),
	}); err != nil {
		return err
	}
	if before.Url != wh.URL {
		if err := s.q.DeleteWebhookDeliveries(ctx, wh.ID); err != nil {
			return fmt.Errorf("clearing webhook %s history: %w", wh.ID, err)
		}
	}
	return nil
}

func (s *Store) DeleteWebhook(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.DeleteWebhookConfig(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting webhook %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("webhook %s: %w", id, auth.ErrNotFound)
	}
	return nil
}

func (s *Store) ListEnabledWebhooks(ctx context.Context) ([]WebhookConfig, error) {
	rows, err := s.q.ListEnabledWebhookConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing enabled webhooks: %w", err)
	}
	paused, err := s.pausedSet(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WebhookConfig, len(rows))
	for i, r := range rows {
		wh, err := webhookFromRow(r)
		if err != nil {
			return nil, fmt.Errorf("listing enabled webhooks: %w", err)
		}
		// Returned, not filtered out: the dispatcher is the one place that
		// decides not to send, so there is a single decision to read.
		markPaused(&wh, paused)
		out[i] = wh
	}
	return out, nil
}

// ListWebhooks returns every webhook, enabled or not, for the admin view. A
// disabled hook has to stay visible there or it can never be switched back
// on; delivery keeps using ListEnabledWebhooks.
func (s *Store) ListWebhooks(ctx context.Context) ([]WebhookConfig, error) {
	rows, err := s.q.ListWebhookConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing webhooks: %w", err)
	}
	paused, err := s.pausedSet(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WebhookConfig, len(rows))
	for i, r := range rows {
		wh, err := webhookFromRow(r)
		if err != nil {
			return nil, fmt.Errorf("listing webhooks: %w", err)
		}
		markPaused(&wh, paused)
		out[i] = wh
	}
	return out, nil
}

// RecordWebhookDelivery stores d as the hook's latest result, unless a newer
// attempt has already been recorded or the hook's URL is no longer url.
func (s *Store) RecordWebhookDelivery(ctx context.Context, id uuid.UUID, url string, d WebhookDelivery) error {
	if err := s.q.RecordWebhookDelivery(ctx, dbgen.RecordWebhookDeliveryParams{
		ID: id, Url: url, At: d.At, Status: int32(d.Status), ErrorClass: d.Error,
	}); err != nil {
		return fmt.Errorf("recording webhook delivery %s: %w", id, err)
	}
	return nil
}

// ── Delivery history (#448) ──────────────────────────────────────────────────

// WebhookHistoryLimit is how many deliveries are kept per hook.
const WebhookHistoryLimit = 25

// WebhookAttempt is one try at a delivery. The delivery is identified by
// DeliveryID, which is the X-GHD-Delivery the receiver saw; a retry is the
// same DeliveryID again.
type WebhookAttempt struct {
	DeliveryID uuid.UUID
	HookID     uuid.UUID
	EventType  string
	Event      []byte // the queued event, kept only so Redeliver can send it again
	Result     WebhookDelivery
	Retrying   bool // a failure the worker will try again
}

// WebhookDeliveryRecord is one row of a hook's history. There is no event and
// no error text: see migration 000037.
type WebhookDeliveryRecord struct {
	ID            uuid.UUID `json:"id"`
	Event         string    `json:"event"`
	StartedAt     time.Time `json:"started_at"`      // the first attempt
	LastAttemptAt time.Time `json:"last_attempt_at"` // the latest
	Status        int       `json:"status"`          // HTTP status; 0 when no response came back
	Result        string    `json:"result"`          // "ok", or one of notify.DeliveryErrors
	Attempts      int       `json:"attempts"`
	Retrying      bool      `json:"retrying"`
	// DropReason is set only when Result is "dropped" (#499): one of
	// notify.DropReasons. Absent from the JSON otherwise.
	DropReason string `json:"drop_reason,omitempty"`
}

// RecordWebhookAttempt stores an attempt on its delivery's row and trims the
// hook's history to WebhookHistoryLimit.
func (s *Store) RecordWebhookAttempt(ctx context.Context, a WebhookAttempt) error {
	result := a.Result.Error
	if result == "" {
		result = "ok"
	}
	if err := s.q.UpsertWebhookDelivery(ctx, dbgen.UpsertWebhookDeliveryParams{
		ID: a.DeliveryID, WebhookID: a.HookID, EventType: a.EventType, Event: a.Event,
		At: a.Result.At, Status: int32(a.Result.Status), Result: result, Retrying: a.Retrying,
	}); err != nil {
		return fmt.Errorf("recording webhook delivery attempt %s: %w", a.DeliveryID, err)
	}
	if err := s.q.PruneWebhookDeliveries(ctx, dbgen.PruneWebhookDeliveriesParams{
		HookID: a.HookID, KeepN: WebhookHistoryLimit,
	}); err != nil {
		return fmt.Errorf("trimming webhook %s history: %w", a.HookID, err)
	}
	return nil
}

// MarkWebhookDeliveryDropped turns a delivery's "retrying" entry into
// "dropped" with the reason (#499). Any other entry, or none, is left as it is.
func (s *Store) MarkWebhookDeliveryDropped(ctx context.Context, hookID, deliveryID uuid.UUID, reason string) error {
	if err := s.q.MarkWebhookDeliveryDropped(ctx, dbgen.MarkWebhookDeliveryDroppedParams{
		ID: deliveryID, WebhookID: hookID, Reason: reason,
	}); err != nil {
		return fmt.Errorf("marking webhook delivery %s dropped: %w", deliveryID, err)
	}
	return nil
}

// ListWebhookDeliveries returns a hook's history, newest first.
func (s *Store) ListWebhookDeliveries(ctx context.Context, hookID uuid.UUID) ([]WebhookDeliveryRecord, error) {
	rows, err := s.q.ListWebhookDeliveries(ctx, hookID)
	if err != nil {
		return nil, fmt.Errorf("listing webhook %s deliveries: %w", hookID, err)
	}
	out := make([]WebhookDeliveryRecord, len(rows))
	for i, r := range rows {
		out[i] = WebhookDeliveryRecord{ID: r.ID, Event: r.EventType,
			StartedAt: r.CreatedAt.UTC(), LastAttemptAt: r.LastAttemptAt.UTC(),
			Status: int(r.Status), Result: r.Result, Attempts: int(r.Attempts), Retrying: r.Retrying, DropReason: r.DropReason}
	}
	return out, nil
}

// GetWebhookDeliveryEvent returns the stored event of one delivery of one
// hook. The error wraps sql.ErrNoRows when there is no such delivery.
func (s *Store) GetWebhookDeliveryEvent(ctx context.Context, hookID, deliveryID uuid.UUID) ([]byte, error) {
	ev, err := s.q.GetWebhookDeliveryEvent(ctx, dbgen.GetWebhookDeliveryEventParams{ID: deliveryID, WebhookID: hookID})
	if err != nil {
		return nil, fmt.Errorf("getting webhook delivery %s: %w", deliveryID, err)
	}
	return ev, nil
}
