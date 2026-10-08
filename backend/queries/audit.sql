-- name: CreateAuditEntry :exec
INSERT INTO audit_log (id, actor_id, entity_type, entity_id, action, before, after, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAuditByEntity :many
SELECT * FROM audit_log
WHERE entity_type = $1 AND entity_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3 OFFSET $4;

-- name: SearchAuditLog :many
-- The admin-wide audit view (#129), unscoped: an administrator, or staff while
-- ticket scope enforcement is off. Every filter is optional; see audit.Filter.
-- Staff with scope enforced use SearchAuditLogScoped.
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
  LIMIT sqlc.arg(count_cap)
) AS matched;

-- name: GetAuditTicketScope :one
-- What a staff member's group membership grants, for the scoped audit
-- statements: their groups, the categories their groups cover whole (rules
-- without a type), and the rules with a type, as the category and the type of
-- each. The same facts ticket.CanView gets from server.staffScopeFor, read
-- here independently so the parity test compares two lookups, not one.
SELECT
  ARRAY(
    SELECT gm.group_id FROM group_members gm
    WHERE gm.user_id = sqlc.arg(user_id)::uuid
  )::uuid[] AS group_ids,
  ARRAY(
    SELECT gs.category_id FROM group_scopes gs
    JOIN group_members gm ON gm.group_id = gs.group_id
    WHERE gm.user_id = sqlc.arg(user_id)::uuid AND gs.type_id IS NULL
  )::uuid[] AS category_ids,
  ARRAY(
    SELECT gs.category_id FROM group_scopes gs
    JOIN group_members gm ON gm.group_id = gs.group_id
    WHERE gm.user_id = sqlc.arg(user_id)::uuid AND gs.type_id IS NOT NULL
  )::uuid[] AS typed_category_ids,
  ARRAY(
    SELECT gs.type_id FROM group_scopes gs
    JOIN group_members gm ON gm.group_id = gs.group_id
    WHERE gm.user_id = sqlc.arg(user_id)::uuid AND gs.type_id IS NOT NULL
  )::uuid[] AS type_ids;

-- name: SearchAuditLogScoped :many
-- SearchAuditLog for a staff member with ticket scope enforced. Same filters,
-- same order; the scope predicate is CountAuditLogScoped's, word for word.
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
  -- Staff scope (#330, #331). Only ticket entries on a ticket this staff
  -- member may see: the same four ways in as ticket.CanView — reported by
  -- them, assigned to them, assigned to a group of theirs, or inside a
  -- Category/Type a group of theirs covers. The groups and rules arrive as
  -- arrays from GetAuditTicketScope; see the note above CountAuditLogScoped
  -- for why they are values and not subqueries. An entry whose ticket is
  -- gone matches nothing, which is what "not yours" looks like too.
  AND entity_type = 'ticket'
  AND EXISTS (
    SELECT 1 FROM tickets t
    WHERE t.id = audit_log.entity_id
      AND (
        t.reporter_user_id = sqlc.arg(user_id)::uuid
        OR t.assignee_user_id = sqlc.arg(user_id)::uuid
        OR t.assignee_group_id = ANY (sqlc.arg(group_ids)::uuid[])
        OR t.category_id = ANY (sqlc.arg(category_ids)::uuid[])
        -- A rule with a type. type_id alone decides it: a type belongs to one
        -- category, and the (category_id, type_id) foreign keys on tickets and
        -- group_scopes (000013) hold both to it. The category test is
        -- redundant on purpose: it lets this arm use tickets_category_id_idx,
        -- so every arm of the OR has an index and the planner can collect a
        -- small visible set with a BitmapOr instead of scanning tickets.
        -- A ticket with no type is not covered here (NULL = ANY is not true).
        OR (t.category_id = ANY (sqlc.arg(typed_category_ids)::uuid[])
            AND t.type_id = ANY (sqlc.arg(type_ids)::uuid[]))
      )
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountAuditLogScoped :one
-- CountAuditLog for a staff member with ticket scope enforced, bounded the
-- same way.
--
-- Why the scope arrives as arrays (#331). The best plan depends on how many
-- tickets the staff member sees, and only the values say that. A staff member
-- who sees five tickets is answered by collecting those five through the
-- tickets indexes and reading their entries through audit_log_entity_idx; one
-- who sees every ticket, with a narrow filter, by reading the filter's index
-- and checking each row's ticket by primary key; with no filter, by walking the
-- log and keeping what is visible. Written as subqueries on the user id, the
-- planner cannot tell these apart and picks one shape for everyone: #353's
-- visible-set-first count paid a full tickets pass on every narrow filter, and
-- the per-row EXISTS before it walked the whole log for a sparse staff member.
-- With the arrays as parameters each execution is planned from them. Measured
-- in #331. plan_cache_mode=auto kept every case on its custom plan; the generic
-- plan is far worse and must stay unchosen (see the benchmark notes).
--
-- The predicate is not wrapped in "user_id IS NULL OR ...": that form cannot
-- become a join, which is what lets the planner start from the tickets side.
-- Administrators use CountAuditLog instead.
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
    -- Staff scope (#330, #331). Only ticket entries on a ticket this staff
    -- member may see: the same four ways in as ticket.CanView — reported by
    -- them, assigned to them, assigned to a group of theirs, or inside a
    -- Category/Type a group of theirs covers. The groups and rules arrive as
    -- arrays from GetAuditTicketScope; see the note above CountAuditLogScoped
    -- for why they are values and not subqueries. An entry whose ticket is
    -- gone matches nothing, which is what "not yours" looks like too.
    AND entity_type = 'ticket'
    AND EXISTS (
      SELECT 1 FROM tickets t
      WHERE t.id = audit_log.entity_id
        AND (
          t.reporter_user_id = sqlc.arg(user_id)::uuid
          OR t.assignee_user_id = sqlc.arg(user_id)::uuid
          OR t.assignee_group_id = ANY (sqlc.arg(group_ids)::uuid[])
          OR t.category_id = ANY (sqlc.arg(category_ids)::uuid[])
          -- A rule with a type. type_id alone decides it: a type belongs to one
          -- category, and the (category_id, type_id) foreign keys on tickets and
          -- group_scopes (000013) hold both to it. The category test is
          -- redundant on purpose: it lets this arm use tickets_category_id_idx,
          -- so every arm of the OR has an index and the planner can collect a
          -- small visible set with a BitmapOr instead of scanning tickets.
          -- A ticket with no type is not covered here (NULL = ANY is not true).
          OR (t.category_id = ANY (sqlc.arg(typed_category_ids)::uuid[])
              AND t.type_id = ANY (sqlc.arg(type_ids)::uuid[]))
        )
    )
  LIMIT sqlc.arg(count_cap)
) AS matched;

-- name: DeleteAuditLogBefore :execrows
-- The retention sweep's hard delete. No archive table — see audit.Store's
-- own comment on DeleteOlderThan for why.
DELETE FROM audit_log WHERE created_at < $1;
