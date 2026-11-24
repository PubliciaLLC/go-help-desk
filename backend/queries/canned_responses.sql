-- name: CreateCannedResponse :one
INSERT INTO canned_responses (id, name, body, category_id, type_id, sort_order)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCannedResponse :one
SELECT * FROM canned_responses WHERE id = $1;

-- name: ListCannedResponses :many
SELECT * FROM canned_responses ORDER BY sort_order, name;

-- name: UpdateCannedResponse :exec
UPDATE canned_responses
SET name = $2, body = $3, category_id = $4, type_id = $5, sort_order = $6
WHERE id = $1;

-- name: DeleteCannedResponse :exec
DELETE FROM canned_responses WHERE id = $1;
