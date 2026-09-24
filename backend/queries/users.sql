-- name: CreateUser :exec
INSERT INTO users (
    id,
    email,
    display_name,
    role,
    password_hash,
    mfa_secret,
    mfa_enabled,
    saml_subject,
    oidc_subject,
    created_at,
    updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 AND deleted_at IS NULL;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1 AND deleted_at IS NULL;

-- name: GetUserBySAMLSubject :one
SELECT * FROM users WHERE saml_subject = $1 AND saml_subject != '' AND deleted_at IS NULL;

-- name: GetUserByOIDCSubject :one

SELECT * FROM users WHERE oidc_subject = $1 AND oidc_subject != '' AND deleted_at IS NULL;

-- name: UpdateUser :exec
UPDATE users
SET email = $2, display_name = $3, role = $4, password_hash = $5,
    mfa_secret = $6,
    mfa_enabled = $7,
    saml_subject = $8,
    oidc_subject = $9,
    updated_at = $10
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = now() WHERE id = $1;

-- name: DisableUser :exec
UPDATE users SET disabled = TRUE, updated_at = now() WHERE id = $1;

-- name: EnableUser :exec
UPDATE users SET disabled = FALSE, updated_at = now() WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users WHERE deleted_at IS NULL AND disabled = FALSE ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CountUsers :one
SELECT COUNT(*) FROM users WHERE deleted_at IS NULL AND disabled = FALSE;

-- name: CountAllUsers :one
-- Every row, including disabled and soft-deleted accounts.
--
-- This is what gates /setup, and the filtered count above is why it had to
-- exist. Soft-delete or disable every account -- which an administrator can do
-- to their own, sole, admin account, since nothing stops them -- and the
-- filtered count returns zero, /setup/status answers {"needed": true}, and
-- anyone on the internet can POST /setup and be handed an administrator over
-- the existing data: every ticket, every customer, every attachment.
--
-- "Setup is permanently blocked once complete" is what the design says. A
-- count of live accounts cannot express "permanently"; a count of rows can,
-- because nothing in this system hard-deletes a user.
SELECT COUNT(*) FROM users;

-- name: GetUserByIDAdmin :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsersAdmin :many
SELECT * FROM users WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: RestoreUser :exec
UPDATE users SET deleted_at = NULL, updated_at = now() WHERE id = $1;

-- name: ClearMFA :exec
UPDATE users SET mfa_secret = '', mfa_enabled = false, updated_at = now() WHERE id = $1;

-- name: AdminSetPassword :exec
UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1;

-- name: ClaimMFAAttempt :one
-- Takes one attempt off the account's TOTP budget, and reports what is left.
--
-- Called BEFORE the code is checked, which is the whole point. The previous
-- order was read the lock, check the code, then count a failure -- three
-- statements, and every request that started before the first UPDATE landed
-- read "not locked" and went on to verify. Measured against the real server:
-- forty parallel wrong codes, thirty-six of them verified, limit five. An
-- attacker holding the password -- the exact case MFA exists for -- got a few
-- hundred guesses per window instead of five.
--
-- One UPDATE has no such window. Concurrent updates of one row serialise in
-- Postgres and each re-reads the row it is updating, so forty requests take
-- the numbers one to forty and the caller refuses everything past the budget.
--
-- Three cases, in the order the CASE tests them:
--
--   locked and still locked   the count keeps rising and the deadline does
--                             NOT move, so an attacker hammering a locked
--                             account cannot hold the owner out forever by
--                             pushing the lock further away.
--   locked and expired        the window is over: back to one, lock cleared.
--                             Without this the count stays at the maximum and
--                             the next single attempt re-locks immediately.
--   not locked                count it, and lock once the budget is spent.
UPDATE users
SET mfa_failed_attempts = CASE
        WHEN mfa_locked_until IS NOT NULL AND mfa_locked_until <= now() THEN 1
        ELSE mfa_failed_attempts + 1
    END,
    mfa_locked_until = CASE
        WHEN mfa_locked_until IS NOT NULL AND mfa_locked_until <= now() THEN NULL
        WHEN mfa_locked_until IS NOT NULL THEN mfa_locked_until
        WHEN mfa_failed_attempts + 1 >= sqlc.arg(max_attempts)::int
            THEN now() + make_interval(secs => sqlc.arg(lock_seconds)::int)
        ELSE mfa_locked_until
    END,
    updated_at = now()
WHERE id = $1
RETURNING mfa_failed_attempts, mfa_locked_until;

-- name: RecordMFAFailure :one
-- Counts a failed TOTP attempt and locks the account once the threshold is
-- reached. Returns the resulting lock time so the caller can refuse
-- immediately without a second round trip.
--
-- The count and the lock are set in one statement so concurrent attempts
-- cannot both read "4 failures" and both decide they are allowed.
UPDATE users
SET mfa_failed_attempts = mfa_failed_attempts + 1,
    mfa_locked_until = CASE
        WHEN mfa_failed_attempts + 1 >= sqlc.arg(max_attempts)::int
        THEN now() + make_interval(secs => sqlc.arg(lock_seconds)::int)
        ELSE mfa_locked_until
    END,
    updated_at = now()
WHERE id = $1
RETURNING mfa_failed_attempts, mfa_locked_until;

-- name: ClearMFAFailures :exec
-- Called after a correct code. NIST SP 800-63B has the verifier disregard
-- prior failed attempts once the user authenticates successfully.
UPDATE users
SET mfa_failed_attempts = 0, mfa_locked_until = NULL, updated_at = now()
WHERE id = $1;

-- name: GetMFALock :one
SELECT mfa_failed_attempts, mfa_locked_until FROM users WHERE id = $1;
