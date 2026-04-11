-- name: CreateAuditEntry :exec
INSERT INTO audit_log (id, actor_id, entity_type, entity_id, action, before, after, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAuditByEntity :many
SELECT * FROM audit_log
WHERE entity_type = $1 AND entity_id = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;
