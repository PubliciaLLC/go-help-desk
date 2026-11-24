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

-- name: CountStatusHistoryByStatus :one
-- Rows in ticket_status_history that reference a status, in either direction.
-- ticket_status_history has foreign keys to statuses with no ON DELETE action,
-- so a status with zero CURRENT tickets can still be undeletable because a past
-- transition mentions it. Counting first turns a raw foreign-key 500 into an
-- explanation the administrator can act on.
SELECT COUNT(*) FROM ticket_status_history
WHERE to_status_id = $1 OR from_status_id = $1;
