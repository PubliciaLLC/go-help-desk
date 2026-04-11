-- name: CreateStatus :exec
INSERT INTO statuses (id, name, kind, sort_order, color) VALUES ($1, $2, $3, $4, $5);

-- name: GetStatus :one
SELECT * FROM statuses WHERE id = $1;

-- name: GetStatusByName :one
SELECT * FROM statuses WHERE name = $1;

-- name: UpdateStatus :exec
UPDATE statuses SET name = $2, sort_order = $3, color = $4 WHERE id = $1;

-- name: DeleteStatus :exec
DELETE FROM statuses WHERE id = $1 AND kind = 'custom';

-- name: ListStatuses :many
SELECT * FROM statuses ORDER BY sort_order, name;
