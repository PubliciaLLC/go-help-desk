-- name: CreateAPIKey :exec
INSERT INTO api_keys (id, name, hashed_token, user_id, scopes, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys WHERE hashed_token = $1;

-- name: UpdateAPIKeyLastUsed :exec
UPDATE api_keys SET last_used_at = $2 WHERE id = $1;

-- name: DeleteAPIKey :exec
DELETE FROM api_keys WHERE id = $1;

-- name: ListAPIKeysByUser :many
SELECT * FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC;

-- name: CreateOAuthClient :exec
INSERT INTO oauth_clients (id, client_id, hashed_secret, name, scopes, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetOAuthClientByClientID :one
SELECT * FROM oauth_clients WHERE client_id = $1;

-- name: DeleteOAuthClient :exec
DELETE FROM oauth_clients WHERE id = $1;

-- name: ListOAuthClients :many
SELECT * FROM oauth_clients ORDER BY name;

-- name: CreateWebhookConfig :exec
INSERT INTO webhook_configs (id, url, events, secret, enabled, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetWebhookConfig :one
SELECT * FROM webhook_configs WHERE id = $1;

-- name: UpdateWebhookConfig :exec
UPDATE webhook_configs SET url = $2, events = $3, secret = $4, enabled = $5 WHERE id = $1;

-- name: DeleteWebhookConfig :exec
DELETE FROM webhook_configs WHERE id = $1;

-- name: ListEnabledWebhookConfigs :many
SELECT * FROM webhook_configs WHERE enabled = TRUE ORDER BY created_at;
