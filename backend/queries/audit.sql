-- name: CreateAuditEntry :exec
INSERT INTO audit_log (id, actor_id, entity_type, entity_id, action, before, after, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAuditByEntity :many
SELECT * FROM audit_log
WHERE entity_type = $1 AND entity_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3 OFFSET $4;

-- name: SearchAuditLog :many
-- The admin-wide audit view (#129), administrators only. Every filter is
-- optional; see audit.Filter.
SELECT * FROM audit_log
WHERE (sqlc.narg(entity_type)::text IS NULL OR entity_type = sqlc.narg(entity_type)::text)
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action)::text)
  AND (sqlc.narg(actor_id)::uuid IS NULL OR actor_id = sqlc.narg(actor_id)::uuid)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR created_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR created_at <= sqlc.narg(to_ts)::timestamptz)
  AND (
    sqlc.narg(q)::text IS NULL
    OR entity_type ILIKE '%' || sqlc.narg(q)::text || '%'
    OR action ILIKE '%' || sqlc.narg(q)::text || '%'
  )
  AND (sqlc.narg(exclude_actions)::text[] IS NULL OR NOT (action = ANY (sqlc.narg(exclude_actions)::text[])))
-- id breaks the tie, because created_at alone does not order entries written
-- in the same microsecond — which happens inside a single request. Without a
-- total order, two pages of one listing can disagree about which tied row
-- comes first and the same entry appears twice, or not at all.
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountAuditLog :one
-- Same filters as SearchAuditLog, but BOUNDED: it counts at most count_cap
-- matches and stops, so the cost is that of finding count_cap rows, not of
-- scanning a table nobody prunes (#331). The caller asks for the cap plus one
-- and reads "more than the cap" off the result; an answer at or under the cap
-- is exact.
SELECT COUNT(*) FROM (
  SELECT 1 FROM audit_log
  WHERE (sqlc.narg(entity_type)::text IS NULL OR entity_type = sqlc.narg(entity_type)::text)
    AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action)::text)
    AND (sqlc.narg(actor_id)::uuid IS NULL OR actor_id = sqlc.narg(actor_id)::uuid)
    AND (sqlc.narg(from_ts)::timestamptz IS NULL OR created_at >= sqlc.narg(from_ts)::timestamptz)
    AND (sqlc.narg(to_ts)::timestamptz IS NULL OR created_at <= sqlc.narg(to_ts)::timestamptz)
    AND (
      sqlc.narg(q)::text IS NULL
      OR entity_type ILIKE '%' || sqlc.narg(q)::text || '%'
      OR action ILIKE '%' || sqlc.narg(q)::text || '%'
    )
    AND (sqlc.narg(exclude_actions)::text[] IS NULL OR NOT (action = ANY (sqlc.narg(exclude_actions)::text[])))
  LIMIT sqlc.arg(count_cap)
) AS matched;

-- name: DeleteAuditLogBefore :execrows
-- The retention sweep's hard delete. No archive table — see audit.Store's
-- own comment on DeleteOlderThan for why.
DELETE FROM audit_log WHERE created_at < $1;
