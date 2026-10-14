-- name: UpsertWebhookDelivery :exec
-- One row per delivery; each attempt updates it. attempts counts them here
-- rather than trusting the caller, so two replicas cannot disagree.
INSERT INTO webhook_deliveries (id, webhook_id, event_type, event, created_at, last_attempt_at, status, result, retrying)
VALUES (sqlc.arg(id), sqlc.arg(webhook_id), sqlc.arg(event_type), sqlc.arg(event), sqlc.arg(at)::timestamptz,
        sqlc.arg(at)::timestamptz, sqlc.arg(status), sqlc.arg(result), sqlc.arg(retrying))
ON CONFLICT (id) DO UPDATE
SET last_attempt_at = EXCLUDED.last_attempt_at,
    status          = EXCLUDED.status,
    result          = EXCLUDED.result,
    retrying        = EXCLUDED.retrying,
    attempts        = webhook_deliveries.attempts + 1
WHERE webhook_deliveries.webhook_id = EXCLUDED.webhook_id;

-- name: PruneWebhookDeliveries :exec
-- Keeps the newest keep_n deliveries of one hook (#448).
DELETE FROM webhook_deliveries
WHERE webhook_deliveries.webhook_id = sqlc.arg(hook_id)
  AND webhook_deliveries.id NOT IN (
      SELECT d.id FROM webhook_deliveries d
      WHERE d.webhook_id = sqlc.arg(hook_id)
      ORDER BY d.created_at DESC, d.id DESC
      LIMIT sqlc.arg(keep_n)
  );

-- name: ListWebhookDeliveries :many
-- No event column: the history shows outcomes, never content.
SELECT id, webhook_id, event_type, created_at, last_attempt_at, status, result, attempts, retrying
FROM webhook_deliveries
WHERE webhook_id = $1
ORDER BY created_at DESC, id DESC;

-- name: GetWebhookDeliveryEvent :one
SELECT event FROM webhook_deliveries WHERE id = $1 AND webhook_id = $2;

-- name: DeleteWebhookDeliveries :exec
DELETE FROM webhook_deliveries WHERE webhook_id = $1;
