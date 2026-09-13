-- name: GetSession :one
-- Only unexpired rows: an expired session must behave exactly like a missing
-- one, so a stale row cannot authenticate anybody between sweeps.
SELECT * FROM sessions WHERE id = $1 AND expires_at > now();

-- name: UpsertSession :exec
INSERT INTO sessions (id, user_id, data, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE
SET user_id    = EXCLUDED.user_id,
    data       = EXCLUDED.data,
    expires_at = EXCLUDED.expires_at,
    updated_at = now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = $1;

-- name: DeleteSessionsForUser :exec
-- Every session a user holds. Used for disable, role change, password change
-- and MFA reset — the events after which a cookie minted under the old state
-- must stop working.
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= now();
