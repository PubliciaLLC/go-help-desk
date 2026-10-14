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

-- name: CreateOAuthClient :exec
-- Creates the client AND its service user (#420) as one statement, so a
-- failure on either leaves neither: a client with no user cannot act, and a
-- user with no client is an orphan nobody can see the reason for.
--
-- The user is named after the client, staff, with no password, no factor and
-- no SSO identity (the users_service_account_inert constraint insists). Its
-- address is made from the client's id: it only has to be unique, and
-- ".invalid" (RFC 2606) can never receive mail. The oauth_clients foreign key
-- is checked at the end of the statement, so inserting both here is fine.
WITH u AS (
    INSERT INTO users (id, email, display_name, role, service_account, created_at, updated_at)
    VALUES (sqlc.arg(user_id), 'oauth-' || sqlc.arg(id)::uuid || '@service-account.invalid',
            sqlc.arg(name), 'staff', TRUE, sqlc.arg(created_at), sqlc.arg(created_at))
)
INSERT INTO oauth_clients (id, client_id, hashed_secret, name, scopes, created_at, user_id)
VALUES (sqlc.arg(id), sqlc.arg(client_id), sqlc.arg(hashed_secret), sqlc.arg(name), sqlc.arg(scopes),
        sqlc.arg(created_at), sqlc.arg(user_id));

-- name: GetOAuthClientByClientID :one
SELECT * FROM oauth_clients WHERE client_id = $1;

-- name: GetOAuthClientWithUser :one
-- The client and the service user it acts as, in one round trip: BearerAuth
-- runs this on every request. A deleted client matches nothing, which is what
-- revokes its outstanding tokens; the user's own state (disabled, deleted)
-- comes back for the caller to judge.
SELECT sqlc.embed(oauth_clients), sqlc.embed(users)
FROM oauth_clients
JOIN users ON users.id = oauth_clients.user_id
WHERE oauth_clients.client_id = $1;

-- name: DeleteOAuthClient :execrows
-- Deletes the client and soft-deletes its service user in one statement, still
-- reporting rows affected so an unknown id stays a 404 (#431). The user is
-- kept, not removed: tickets and replies it wrote keep their author's name.
WITH soft AS (
    UPDATE users SET deleted_at = now(), updated_at = now()
    WHERE users.id = (SELECT user_id FROM oauth_clients WHERE oauth_clients.id = $1)
      AND deleted_at IS NULL
)
DELETE FROM oauth_clients WHERE oauth_clients.id = $1;

-- name: ListOAuthClients :many
SELECT * FROM oauth_clients ORDER BY name;

-- name: GetOAuthClientNameByUser :one
-- The client a service user belongs to, for the message that says to delete
-- the client instead.
SELECT name FROM oauth_clients WHERE user_id = $1;

-- name: ListOAuthClientGroups :many
-- Every client's groups in one query, for the client list: a client's groups
-- are its service user's, and asking per row would be a query per client.
SELECT c.id AS client_row_id, g.id AS group_id, g.name AS group_name
FROM oauth_clients c
JOIN group_members gm ON gm.user_id = c.user_id
JOIN groups g ON g.id = gm.group_id
ORDER BY g.name;

-- name: ListOAuthClientsWithoutGroups :many
-- The clients that will act and see nothing once ticket scope is enforced: a
-- staff member's scope is their groups', so a client whose user is in none
-- sees no ticket. A switched-off client is not one of them (it acts as
-- nobody), which is what lets the warning clear by disabling it.
SELECT c.name
FROM oauth_clients c
JOIN users u ON u.id = c.user_id
WHERE u.deleted_at IS NULL
  AND u.disabled = FALSE
  AND NOT EXISTS (SELECT 1 FROM group_members gm WHERE gm.user_id = u.id)
ORDER BY c.name;

-- name: CreateWebhookConfig :exec
INSERT INTO webhook_configs (id, url, events, secret, enabled, created_at, payload_format, ticket_filter)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetWebhookConfig :one
SELECT * FROM webhook_configs WHERE id = $1;

-- name: UpdateWebhookConfig :exec
-- A new URL clears the last delivery result: it described a different target.
-- (On the right-hand side of SET, "url" is the OLD value.)
UPDATE webhook_configs
SET url = $2, events = $3, secret = $4, enabled = $5, payload_format = $6, ticket_filter = $7,
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
