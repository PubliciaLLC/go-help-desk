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
