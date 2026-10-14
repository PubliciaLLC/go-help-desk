-- name: CreateAPIKey :exec
INSERT INTO api_keys (id, name, hashed_token, user_id, scopes, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys WHERE hashed_token = $1;

-- name: UpdateAPIKeyLastUsed :exec
UPDATE api_keys SET last_used_at = $2 WHERE id = $1;

-- name: DeleteAPIKey :execrows
DELETE FROM api_keys WHERE id = $1;

-- name: ListAPIKeysByUser :many
SELECT * FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC;

-- name: DeleteAPIKeyForUser :execrows
-- Scoped to the owner: a credential that is not an administrator signed in
-- through the browser may revoke only its own owner's keys. A key that exists
-- but belongs to someone else deletes nothing, which the caller reports as not
-- found.
DELETE FROM api_keys WHERE id = $1 AND user_id = $2;

-- name: ListAPIKeysWithOwner :many
-- user_id NULL lists every key; a value lists that user's. The owner comes
-- from the same row set so the admin page can show whose each key is.
SELECT k.*, u.email AS owner_email, u.display_name AS owner_name
FROM api_keys k
JOIN users u ON u.id = k.user_id
WHERE (sqlc.narg(user_id)::uuid IS NULL OR k.user_id = sqlc.narg(user_id)::uuid)
ORDER BY k.created_at DESC;

-- name: CreateOAuthClient :exec
INSERT INTO oauth_clients (id, client_id, hashed_secret, name, scopes, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetOAuthClientByClientID :one
SELECT * FROM oauth_clients WHERE client_id = $1;

-- name: DeleteOAuthClient :execrows
DELETE FROM oauth_clients WHERE id = $1;

-- name: ListOAuthClients :many
SELECT * FROM oauth_clients ORDER BY name;

-- name: CreateWebhookConfig :exec
INSERT INTO webhook_configs (id, url, events, secret, enabled, created_at, payload_format)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetWebhookConfig :one
SELECT * FROM webhook_configs WHERE id = $1;

-- name: UpdateWebhookConfig :exec
-- A new URL clears the last delivery result: it described a different target.
-- (On the right-hand side of SET, "url" is the OLD value.)
UPDATE webhook_configs
SET url = $2, events = $3, secret = $4, enabled = $5, payload_format = $6,
    last_delivery_at     = CASE WHEN url = $2 THEN last_delivery_at END,
    last_delivery_status = CASE WHEN url = $2 THEN last_delivery_status ELSE 0 END,
    last_delivery_error  = CASE WHEN url = $2 THEN last_delivery_error ELSE '' END
WHERE id = $1;

-- name: DeleteWebhookConfig :execrows
DELETE FROM webhook_configs WHERE id = $1;

-- name: ListEnabledWebhookConfigs :many
SELECT * FROM webhook_configs WHERE enabled = TRUE ORDER BY created_at;

-- name: ListWebhookConfigs :many
-- The admin view: every subscription, enabled or not. The dispatcher keeps
-- using ListEnabledWebhookConfigs.
SELECT * FROM webhook_configs ORDER BY created_at;

-- name: RecordWebhookDelivery :exec
-- Deliveries to one hook run concurrently, across goroutines and replicas.
-- The attempt that STARTED latest wins, so a slow timeout cannot overwrite a
-- newer success. Matching on url drops a result for a URL the hook no longer
-- has. A deleted hook matches nothing, which is fine.
UPDATE webhook_configs
SET last_delivery_at     = sqlc.arg(at)::timestamptz,
    last_delivery_status = sqlc.arg(status),
    last_delivery_error  = sqlc.arg(error_class)
WHERE id = sqlc.arg(id)
  AND url = sqlc.arg(url)
  AND (last_delivery_at IS NULL OR last_delivery_at <= sqlc.arg(at)::timestamptz);
