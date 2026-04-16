-- name: CreateUser :exec
INSERT INTO users (id, email, display_name, role, password_hash, mfa_secret, mfa_enabled, saml_subject, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 AND deleted_at IS NULL;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1 AND deleted_at IS NULL;

-- name: GetUserBySAMLSubject :one
SELECT * FROM users WHERE saml_subject = $1 AND saml_subject != '' AND deleted_at IS NULL;

-- name: UpdateUser :exec
UPDATE users
SET email = $2, display_name = $3, role = $4, password_hash = $5,
    mfa_secret = $6, mfa_enabled = $7, saml_subject = $8, updated_at = $9
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
