-- name: CreateSLAPolicy :exec
INSERT INTO sla_policies (id, name, priority, category_id, response_target_min, resolution_target_min)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetSLAPolicy :one
SELECT * FROM sla_policies WHERE id = $1;

-- name: UpdateSLAPolicy :exec
UPDATE sla_policies
SET name = $2, priority = $3, category_id = $4,
    response_target_min = $5, resolution_target_min = $6
WHERE id = $1;

-- name: DeleteSLAPolicy :exec
DELETE FROM sla_policies WHERE id = $1;

-- name: ListSLAPolicies :many
SELECT * FROM sla_policies ORDER BY priority, name;

-- name: FindSLAPolicy :one
-- Category-specific policy takes precedence over a global one (category_id IS NULL).
SELECT * FROM sla_policies
WHERE priority = $1
  AND (category_id = $2 OR category_id IS NULL)
ORDER BY category_id NULLS LAST
LIMIT 1;

-- name: CreateSLARecord :exec
INSERT INTO sla_records (ticket_id, policy_id, first_response_at, resolved_at, response_breached_at, resolution_breached_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetSLARecord :one
SELECT * FROM sla_records WHERE ticket_id = $1;

-- name: UpdateSLARecord :exec
UPDATE sla_records
SET first_response_at = $2, resolved_at = $3,
    response_breached_at = $4, resolution_breached_at = $5
WHERE ticket_id = $1;
