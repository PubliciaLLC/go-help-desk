-- name: EnqueueNotification :exec
-- A repeat of an id is ignored, so the webhook fan-out, whose row ids derive
-- from the fan-out row, can be re-run after a crash without queueing a hook
-- twice (#448).
INSERT INTO notification_outbox (id, channel, event) VALUES ($1, $2, $3)
ON CONFLICT (id) DO NOTHING;

-- name: ClaimNotifications :many
-- Takes up to page_limit due rows for this worker. SKIP LOCKED lets several
-- replicas claim at once without two taking the same row; the lease
-- (claimed_until) returns a row to the pool if its worker dies mid-send.
-- attempts counts the claim, so a row that crashes its worker every time
-- still runs out of attempts.
UPDATE notification_outbox
SET claimed_until = now() + make_interval(secs => sqlc.arg(lease_seconds)::int),
    attempts = attempts + 1
WHERE id IN (
    SELECT id FROM notification_outbox
    WHERE failed_at IS NULL
      AND available_at <= now()
      AND (claimed_until IS NULL OR claimed_until < now())
    ORDER BY available_at
    LIMIT sqlc.arg(page_limit)
    FOR UPDATE SKIP LOCKED
)
RETURNING id, channel, event, attempts;

-- name: DeleteNotification :exec
DELETE FROM notification_outbox WHERE id = $1;

-- name: RetryNotification :exec
UPDATE notification_outbox
SET available_at = $2, last_error = $3, claimed_until = NULL
WHERE id = $1;

-- name: FailNotification :exec
UPDATE notification_outbox
SET failed_at = now(), last_error = $2, claimed_until = NULL
WHERE id = $1;

-- name: RewriteNotificationEvent :exec
-- Replaces a row's event. The webhook dispatcher clears the content of a row
-- whose delivery has finally failed, so it does not sit for the 30 days failed
-- rows are kept (#448).
UPDATE notification_outbox SET event = $2 WHERE id = $1;

-- name: DeleteFailedNotificationsBefore :execrows
DELETE FROM notification_outbox WHERE failed_at < $1;

-- name: EmailOutboxHealth :one
-- What the admin email warning reads (#456): email and signup-verification rows that are retrying after
-- an error, or gave up in the last day, and the newest of their error texts.
-- Delivered rows are deleted, so a healthy mailer leaves nothing to count, and
-- a failed row older than a day no longer says anything about now.
SELECT count(*)::bigint AS failing,
       COALESCE((array_agg(last_error ORDER BY COALESCE(failed_at, created_at) DESC))[1], '')::text AS last_error
FROM notification_outbox
WHERE channel IN ('email', 'verification')
  AND last_error IS NOT NULL
  AND (failed_at IS NULL OR failed_at > now() - interval '24 hours');
