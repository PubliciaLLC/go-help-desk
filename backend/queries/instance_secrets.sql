-- name: InsertInstanceSecretIfAbsent :exec
INSERT INTO instance_secrets (name, value) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING;

-- name: GetInstanceSecret :one
SELECT value FROM instance_secrets WHERE name = $1;
