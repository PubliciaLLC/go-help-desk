-- name: UpsertPendingRegistration :one
INSERT INTO pending_registrations (id, email, display_name, password_hash, token, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (lower(email)) DO UPDATE
    SET display_name  = EXCLUDED.display_name,
        password_hash = EXCLUDED.password_hash,
        token         = EXCLUDED.token,
        expires_at    = EXCLUDED.expires_at,
        created_at    = EXCLUDED.created_at
RETURNING *;

-- name: GetPendingRegistrationByToken :one
SELECT * FROM pending_registrations WHERE token = $1;

-- name: DeletePendingRegistration :exec
DELETE FROM pending_registrations WHERE id = $1;
