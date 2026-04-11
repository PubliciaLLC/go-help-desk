-- name: CreatePlugin :exec
INSERT INTO plugins (id, name, version, description, author, runtime, hooks, enabled, wasm_path, installed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetPlugin :one
SELECT * FROM plugins WHERE id = $1;

-- name: UpdatePlugin :exec
UPDATE plugins SET name = $2, version = $3, enabled = $4, wasm_path = $5 WHERE id = $1;

-- name: DeletePlugin :exec
DELETE FROM plugins WHERE id = $1;

-- name: ListPlugins :many
SELECT * FROM plugins ORDER BY name;
