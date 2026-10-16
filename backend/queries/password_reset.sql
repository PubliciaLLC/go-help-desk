-- name: IssuePasswordResetToken :exec
-- Makes the hash the account's one live token, replacing any earlier. Only the
-- hash is stored (#451).
--
-- The lifetime is a duration in seconds, and the deadline is computed by the
-- database from clock_timestamp(), for the reason UpsertSession spells out: one
-- clock decides the lifetime, and now() would be the start of the transaction.
INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, clock_timestamp() + make_interval(secs => sqlc.arg(ttl_seconds)::int))
ON CONFLICT (user_id) DO UPDATE
SET token_hash = EXCLUDED.token_hash,
    expires_at = EXCLUDED.expires_at,
    created_at = now();

-- name: RedeemPasswordResetToken :one
-- Spends a reset link and applies it, as one statement (#451).
--
-- Three things must happen together or not at all: the link is used up, the
-- password changes, and every session the account holds ends. Done as three
-- statements, a failure between them leaves a spent link and an old password,
-- or a new password with the old sessions still signed in. A data-modifying CTE
-- commits or rolls back as a whole (see ClearFactors for the same shape).
--
-- The link is deleted whether or not the account can take the change, so a
-- link for an account that was disabled or deleted after the mail went out is
-- spent and does nothing. No row back means the link did not work for any
-- reason: unknown, used, replaced, expired, or an account that cannot have its
-- password set. The caller does not tell those apart.
--
-- Also clears must_change_password: this is the account holder choosing a
-- password, which is what that flag is waiting for (#497).
WITH spent AS (
    DELETE FROM password_reset_tokens
    WHERE password_reset_tokens.token_hash = $1
      AND password_reset_tokens.expires_at > clock_timestamp()
    RETURNING password_reset_tokens.user_id
), changed AS (
    UPDATE users
    SET password_hash = $2, must_change_password = false, updated_at = now()
    FROM spent
    WHERE users.id = spent.user_id
      AND users.deleted_at IS NULL
      AND NOT users.disabled
      AND NOT users.service_account
    RETURNING users.id
), ended AS (
    DELETE FROM sessions WHERE sessions.user_id IN (SELECT id FROM changed) RETURNING sessions.id
)
SELECT id FROM changed;
