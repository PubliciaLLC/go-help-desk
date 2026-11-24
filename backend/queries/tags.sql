-- name: ListActiveTags :many
SELECT * FROM tags WHERE deleted_at IS NULL ORDER BY name;

-- name: ListAllTags :many
SELECT * FROM tags ORDER BY name;

-- name: SearchActiveTags :many
SELECT * FROM tags WHERE deleted_at IS NULL AND name LIKE $1 ORDER BY name LIMIT 20;

-- name: GetTagByName :one
SELECT * FROM tags WHERE name = $1;

-- name: CreateTag :one
INSERT INTO tags (name) VALUES ($1) RETURNING *;

-- name: SoftDeleteTag :exec
UPDATE tags SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: RestoreTag :exec
UPDATE tags SET deleted_at = NULL WHERE id = $1;

-- name: ListTicketTags :many
SELECT t.* FROM tags t
JOIN ticket_tags tt ON tt.tag_id = t.id
WHERE tt.ticket_id = $1
ORDER BY t.name;

-- name: AddTicketTag :exec
INSERT INTO ticket_tags (ticket_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: RemoveTicketTag :exec
DELETE FROM ticket_tags WHERE ticket_id = $1 AND tag_id = $2;
