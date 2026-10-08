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
-- The role comes back with it, and it is the one in the database rather than
-- the one in the cookie.
--
-- The session payload carries a role, written when the session was minted.
-- That made the cookie the authority on what somebody may do, and it went
-- stale exactly when it mattered: demoting an administrator revokes their
-- sessions, but a request already in flight — a password change, which the
-- account holder can time and which spends 50-odd milliseconds hashing —
-- finished afterwards and minted a NEW session carrying the role it had read
-- on the way in. The demotion was in the database and the attacker was an
-- administrator again. Measured: the demotion landed 52ms into a 93ms
-- request, and the re-issued cookie read /admin/users afterwards.
--
-- Disabling and deleting were never vulnerable to this, because the join
-- below already drops those rows on every request. Role was the one piece of
-- authority left being carried rather than looked up.
SELECT sqlc.embed(s), u.role AS user_role FROM sessions s
LEFT JOIN users u ON u.id = s.user_id
WHERE s.id = $1
  AND s.expires_at > clock_timestamp()
  AND (s.user_id IS NULL OR (u.disabled = FALSE AND u.deleted_at IS NULL));

-- name: UpsertSession :exec
-- The lifetime is a duration in seconds, not a timestamp, so that expires_at is
-- computed by the database — from clock_timestamp(), not now().
--
-- It used to arrive as an absolute time from Go's clock while GetSession and
-- DeleteExpiredSessions compared it against Postgres now(). Two clocks decided
-- one lifetime: any skew between the application host and the database shortened
-- every session by the offset, and skew larger than the session lifetime made
-- every session expire the moment it was written — a login that appears to
-- succeed and then does not, with nothing in the logs to say why. Nothing here
-- needs the application's clock, so it no longer uses it.
--
-- clock_timestamp() rather than now(), because now() is transaction-scoped: it
-- returns the transaction's START time, so saving a session inside a
-- transaction that has been open a while silently shortens it. Measured: a
-- 3-second lifetime written 2 seconds into a transaction comes out as 0.99
-- seconds, and a transaction older than the lifetime would write a row that is
-- already expired — a login that reports success and then does not work.
-- clock_timestamp() is the real time at statement execution and does not care
-- how old the transaction is. The read and the sweep below use it for the same
-- reason inverted: a frozen, earlier now() would keep an expired session
-- loading.
INSERT INTO sessions (id, user_id, data, expires_at)
VALUES ($1, $2, $3, clock_timestamp() + make_interval(secs => sqlc.arg(lifetime_seconds)::int))
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
DELETE FROM sessions WHERE expires_at <= clock_timestamp();

-- name: SpendSAMLHandover :execrows
-- One row affected: this is the cookie's first use. Zero: it was spent before.
-- ON CONFLICT rather than SELECT-then-INSERT, so that two concurrent requests
-- carrying the same cookie cannot both see "not spent" (#337).
INSERT INTO spent_saml_handovers (token_hash) VALUES ($1)
ON CONFLICT (token_hash) DO NOTHING;

-- name: DeleteStaleSAMLHandovers :exec
-- A row only has to outlive the cookie it records. The cookie lives 5 minutes
-- (auth.SAMLHandoverMaxAge). A day leaves a wide margin for clock differences
-- between the replica that minted the cookie (whose clock sets its expiry) and
-- this database (whose clock sets spent_at).
DELETE FROM spent_saml_handovers WHERE spent_at < clock_timestamp() - interval '1 day';
