-- name: GetSession :one
-- Only unexpired rows: an expired session must behave exactly like a missing
-- one, so a stale row cannot authenticate anybody between sweeps.
--
-- The join makes a disabled or deleted user's session behave the same way.
-- Disabling already deletes a user's sessions, but that is a write racing the
-- login it is meant to stop: a disable landing between the password check and
-- the session INSERT deleted nothing and left a live session behind. Deciding
-- it here instead means there is no window to lose — the row simply does not
-- load. This costs no extra round trip, since the session lookup already
-- queries the database on every authenticated request.
--
-- LEFT JOIN, and user_id IS NULL passes: the OIDC flow writes state (nonce,
-- PKCE verifier) into a session before anybody has authenticated, and an inner
-- join would drop those and break the login it is protecting.
SELECT sqlc.embed(s) FROM sessions s
LEFT JOIN users u ON u.id = s.user_id
WHERE s.id = $1
  AND s.expires_at > now()
  AND (s.user_id IS NULL OR (u.disabled = FALSE AND u.deleted_at IS NULL));

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
