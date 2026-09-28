-- name: CreateAuditEntry :exec
INSERT INTO audit_log (id, actor_id, entity_type, entity_id, action, before, after, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAuditByEntity :many
SELECT * FROM audit_log
WHERE entity_type = $1 AND entity_id = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: SearchAuditLog :many
-- The admin-wide audit view (#129). Every filter is optional; a caller that
-- must not see every entity (a scoped staff viewer) filters the result
-- afterwards — see audit.Filter's own comment on why that is not done here.
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
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountAuditLog :one
-- Same filters as SearchAuditLog, without the pagination — the admin-wide
-- view's "n of m" needs the total across every page, not just the one it
-- fetched.
SELECT COUNT(*) FROM audit_log
WHERE (sqlc.narg(entity_type)::text IS NULL OR entity_type = sqlc.narg(entity_type)::text)
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action)::text)
  AND (sqlc.narg(actor_id)::uuid IS NULL OR actor_id = sqlc.narg(actor_id)::uuid)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR created_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR created_at <= sqlc.narg(to_ts)::timestamptz)
  AND (
    sqlc.narg(q)::text IS NULL
    OR entity_type ILIKE '%' || sqlc.narg(q)::text || '%'
    OR action ILIKE '%' || sqlc.narg(q)::text || '%'
  );

-- name: DeleteAuditLogBefore :execrows
-- The retention sweep's hard delete. No archive table — see audit.Store's
-- own comment on DeleteOlderThan for why.
DELETE FROM audit_log WHERE created_at < $1;
