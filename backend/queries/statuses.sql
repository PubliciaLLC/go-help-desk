-- name: CreateStatus :exec
INSERT INTO statuses (id, name, kind, sort_order, color) VALUES ($1, $2, $3, $4, $5);

-- name: GetStatus :one
SELECT * FROM statuses WHERE id = $1;

-- name: GetStatusByName :one
SELECT * FROM statuses WHERE name = $1;

-- name: UpdateStatus :exec
UPDATE statuses SET name = $2, sort_order = $3, color = $4, active = $5 WHERE id = $1;

-- name: DeleteStatus :exec
DELETE FROM statuses WHERE id = $1 AND kind = 'custom';

-- name: ListStatuses :many
SELECT * FROM statuses ORDER BY sort_order, name;

-- name: CountTicketsByStatus :one
SELECT COUNT(*) FROM tickets WHERE status_id = $1;

-- name: CountTicketsByStatusForReporter :one
SELECT COUNT(*) FROM tickets
WHERE status_id = $1 AND reporter_user_id = $2;

-- name: CountTicketsByStatusForAssignee :one
SELECT COUNT(*) FROM tickets
WHERE status_id = $1
  AND (assignee_user_id = $2 OR assignee_group_id = ANY(sqlc.arg('group_ids')::uuid[]));
