-- name: UpsertPendingRegistration :one
-- No password (#360) and no display name (#374): both are chosen at
-- verification, so a second signup for the same address can only re-issue
-- the link, never choose anything the account will get.
INSERT INTO pending_registrations (id, email, token, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (lower(email)) DO UPDATE
    SET token         = EXCLUDED.token,
        expires_at    = EXCLUDED.expires_at,
        created_at    = EXCLUDED.created_at
RETURNING *;

-- name: GetPendingRegistrationByToken :one
SELECT * FROM pending_registrations WHERE token = $1;

-- name: DeletePendingRegistration :exec
DELETE FROM pending_registrations WHERE id = $1;

-- name: GetPendingRegistrationByID :one
-- The send-time read for a queued verification email (#348): the row is read
-- when the mail goes out, so the token and address are the current ones and
-- the token is never copied into the outbox.
SELECT * FROM pending_registrations WHERE id = $1;
