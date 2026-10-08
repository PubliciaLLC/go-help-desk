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
-- Same filters as SearchAuditLog, scope included, but BOUNDED: it counts at most
-- count_cap matches and stops, so the cost is that of finding count_cap rows,
-- not of scanning a table nobody prunes (#331). The caller asks for the cap
-- plus one and reads "more than the cap" off the result; an answer at or under
-- the cap is exact. Scope applies here too, so a staff member is counted only
-- what they can see.
--
-- The staff scope is the same predicate as SearchAuditLog's, over the same
-- tickets columns, but arranged the other way round: the visible tickets are
-- found first, then the audit rows on them. As SearchAuditLog's per-row EXISTS
-- inside a LIMIT the planner assumes matches are plentiful and walks the whole
-- entity index looking for them; for a staff member who sees few tickets that
-- was measured at 3x slower than the unbounded count (411 ms against 143 ms at
-- 524k audit rows / 30k tickets), which is the opposite of a bound. The
-- MATERIALIZED set is empty, and never read, when scoped_to is NULL.
--
-- The Category/Type rule is written as two IN lists, not as the per-ticket
-- EXISTS the page query uses, so each list is built once and the set costs one
-- pass over tickets rather than one subplan run per ticket (measured: a staff
-- count with a narrow filter fell from ~125-165 ms to ~5-40 ms). The meaning is
-- the same: a rule without a type covers its whole category, tickets without a
-- type included; a rule with a type covers that type only, and the row
-- comparison is not true for a ticket whose type is NULL, which stays hidden.
-- IN does not repeat a ticket reached by several groups. The tests in
-- auditstore_scope_rules_test.go state this in Go and hold both queries to it.
WITH visible AS MATERIALIZED (
  SELECT t.id FROM tickets t
  WHERE sqlc.narg(scoped_to)::uuid IS NOT NULL
    AND (
      t.reporter_user_id = sqlc.narg(scoped_to)::uuid
      OR t.assignee_user_id = sqlc.narg(scoped_to)::uuid
      OR t.assignee_group_id IN (SELECT gm.group_id FROM group_members gm WHERE gm.user_id = sqlc.narg(scoped_to)::uuid)
      OR t.category_id IN (
        SELECT gs.category_id FROM group_scopes gs
        JOIN group_members gm ON gm.group_id = gs.group_id
        WHERE gm.user_id = sqlc.narg(scoped_to)::uuid AND gs.type_id IS NULL
      )
      OR (t.category_id, t.type_id) IN (
        SELECT gs.category_id, gs.type_id FROM group_scopes gs
        JOIN group_members gm ON gm.group_id = gs.group_id
        WHERE gm.user_id = sqlc.narg(scoped_to)::uuid AND gs.type_id IS NOT NULL
      )
    )
)
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
    -- Staff scope (#330): NULL means no restriction; otherwise ticket entries
    -- on a visible ticket only. An entry whose ticket is gone matches nothing,
    -- which is what "not yours" looks like too.
    AND (
      sqlc.narg(scoped_to)::uuid IS NULL
      OR (entity_type = 'ticket' AND entity_id IN (SELECT id FROM visible))
    )
  LIMIT sqlc.arg(count_cap)
) AS matched;

-- name: DeleteAuditLogBefore :execrows
-- The retention sweep's hard delete. No archive table — see audit.Store's
-- own comment on DeleteOlderThan for why.
DELETE FROM audit_log WHERE created_at < $1;
