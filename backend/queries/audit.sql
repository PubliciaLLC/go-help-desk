-- name: CreateAuditEntry :exec
INSERT INTO audit_log (id, actor_id, entity_type, entity_id, action, before, after, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAuditByEntity :many
SELECT * FROM audit_log
WHERE entity_type = $1 AND entity_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3 OFFSET $4;

-- name: SearchAuditLog :many
-- The admin-wide audit view (#129). Every filter is optional, and scoped_to
-- narrows to the tickets one staff member may see; see audit.Filter.
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
  -- Staff scope (#330). NULL means no restriction. Otherwise only ticket
  -- entries on a ticket the staff member with this id may see: the same four
  -- ways in as ListTicketsFiltered and ticket.CanView — reported by them,
  -- assigned to them, assigned to a group of theirs, or inside a Category/Type
  -- a group of theirs covers. An entry whose ticket is gone matches nothing
  -- here, which is what "not yours" looks like too.
  AND (
    sqlc.narg(scoped_to)::uuid IS NULL
    OR (
      entity_type = 'ticket'
      AND EXISTS (
        SELECT 1 FROM tickets t
        WHERE t.id = audit_log.entity_id
          AND (
            t.reporter_user_id = sqlc.narg(scoped_to)::uuid
            OR t.assignee_user_id = sqlc.narg(scoped_to)::uuid
            OR t.assignee_group_id IN (SELECT gm.group_id FROM group_members gm WHERE gm.user_id = sqlc.narg(scoped_to)::uuid)
            OR EXISTS (
              SELECT 1 FROM group_scopes gs
              JOIN group_members gm ON gm.group_id = gs.group_id
              WHERE gm.user_id = sqlc.narg(scoped_to)::uuid
                AND gs.category_id = t.category_id
                AND (gs.type_id IS NULL OR gs.type_id = t.type_id)
            )
          )
      )
    )
  )
-- id breaks the tie, because created_at alone does not order entries written
-- in the same microsecond — which happens inside a single request. Without a
-- total order, two pages of one listing can disagree about which tied row
-- comes first and the same entry appears twice, or not at all.
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountAuditLog :one
-- Same filters as SearchAuditLog, scope included, without the pagination — the
-- admin-wide view's "n of m" needs the total across every page, not just the
-- one it fetched. Scope applies here too, so a staff member is counted only
-- what they can see.
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
  )
  -- Staff scope (#330). NULL means no restriction. Otherwise only ticket
  -- entries on a ticket the staff member with this id may see: the same four
  -- ways in as ListTicketsFiltered and ticket.CanView — reported by them,
  -- assigned to them, assigned to a group of theirs, or inside a Category/Type
  -- a group of theirs covers. An entry whose ticket is gone matches nothing
  -- here, which is what "not yours" looks like too.
  AND (
    sqlc.narg(scoped_to)::uuid IS NULL
    OR (
      entity_type = 'ticket'
      AND EXISTS (
        SELECT 1 FROM tickets t
        WHERE t.id = audit_log.entity_id
          AND (
            t.reporter_user_id = sqlc.narg(scoped_to)::uuid
            OR t.assignee_user_id = sqlc.narg(scoped_to)::uuid
            OR t.assignee_group_id IN (SELECT gm.group_id FROM group_members gm WHERE gm.user_id = sqlc.narg(scoped_to)::uuid)
            OR EXISTS (
              SELECT 1 FROM group_scopes gs
              JOIN group_members gm ON gm.group_id = gs.group_id
              WHERE gm.user_id = sqlc.narg(scoped_to)::uuid
                AND gs.category_id = t.category_id
                AND (gs.type_id IS NULL OR gs.type_id = t.type_id)
            )
          )
      )
    )
  );

-- name: DeleteAuditLogBefore :execrows
-- The retention sweep's hard delete. No archive table — see audit.Store's
-- own comment on DeleteOlderThan for why.
DELETE FROM audit_log WHERE created_at < $1;
