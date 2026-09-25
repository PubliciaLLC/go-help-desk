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
-- The four tiers DESIGN.md documents, most specific first:
--   1. Priority + Category
--   2. Priority only      (category_id IS NULL = any category)
--   3. Category only      (priority IS NULL = any priority)
--   4. Catch-all          (neither set)
--
-- A NULL column means "matches anything", so the WHERE admits every candidate
-- and the ORDER BY picks the most specific. Before this, priority was NOT NULL
-- and the predicate required an exact match, so tiers 3 and 4 could neither be
-- stored nor matched.
SELECT * FROM sla_policies
WHERE (priority IS NULL OR priority = sqlc.arg(priority)::text)
  AND (category_id IS NULL OR category_id = sqlc.arg(category_id)::uuid)
ORDER BY
  CASE
    WHEN priority IS NOT NULL AND category_id IS NOT NULL THEN 1
    WHEN priority IS NOT NULL THEN 2
    WHEN category_id IS NOT NULL THEN 3
    ELSE 4
  END
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

-- name: ListSLABreachCandidates :many
-- Tickets the breach sweep must evaluate: open, under a policy, with at least
-- one target that is neither met nor already stamped. The age check is a
-- necessary condition only: elapsed-toward-target can never exceed wall-clock
-- age (pausing only subtracts), so a ticket younger than its target cannot
-- have breached under any accounting. The sufficient check — pause-aware — is
-- EvaluateBreaches' job, not this query's.
SELECT r.ticket_id
FROM sla_records r
JOIN tickets      t ON t.id = r.ticket_id
JOIN sla_policies p ON p.id = r.policy_id
WHERE t.closed_at IS NULL
  AND (
       (r.first_response_at IS NULL AND r.response_breached_at IS NULL
          AND t.created_at + make_interval(mins => p.response_target_min) <= sqlc.arg(now)::timestamptz)
    OR (r.resolved_at IS NULL AND r.resolution_breached_at IS NULL
          AND t.created_at + make_interval(mins => p.resolution_target_min) <= sqlc.arg(now)::timestamptz)
  )
ORDER BY t.created_at;

-- name: ListSLARecordsByTicketIDs :many
-- Batch lookup for the per-ticket SLA status embedded on GET /tickets and
-- GET /tickets/{id} (#183): one query for the whole page, after it is
-- sliced, rather than a JOIN pushed into every one of the ~12 list/search
-- queries that would compute SLA for limit×(1+groups) rows and throw most
-- of them away. See sla.Service.StatusesFor.
SELECT * FROM sla_records WHERE ticket_id = ANY(sqlc.arg('ticket_ids')::uuid[]);

-- name: StampSLABreaches :exec
-- Sets only the breach columns, and only where still NULL. Two evaluators
-- racing on the same row cannot overwrite each other's stamp or, worse, the
-- request path's first_response_at / resolved_at. A stamp, once set, is a
-- fact about what happened (DESIGN.md) and is never cleared here.
UPDATE sla_records
SET response_breached_at   = COALESCE(response_breached_at,   sqlc.narg(response_breached_at)::timestamptz),
    resolution_breached_at = COALESCE(resolution_breached_at, sqlc.narg(resolution_breached_at)::timestamptz)
WHERE ticket_id = $1;
