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
    sqlc.narg(q_pattern)::text IS NULL
    OR entity_type ILIKE '%' || sqlc.narg(q_pattern)::text || '%'
    OR action ILIKE '%' || sqlc.narg(q_pattern)::text || '%'
    -- The actor's name and email (#629, D34). Uncorrelated, so it is evaluated
    -- once and probed by actor_id, not once per row. A deleted account is not
    -- named by the view, so it is not matched either; a requester is left out
    -- while the mask is on, so the search is not a way round it.
    OR actor_id IN (
      SELECT u.id FROM users u
      WHERE u.deleted_at IS NULL
        AND (u.display_name ILIKE '%' || sqlc.narg(q_pattern)::text || '%'
             OR u.email ILIKE '%' || sqlc.narg(q_pattern)::text || '%')
        AND NOT (sqlc.arg(mask_requesters)::bool AND u.role = 'user')
    )
    -- The change details (#629, D33), at the keys audit.Redact shows and nowhere
    -- else. diff_path is built by auditstore.diffPath: a strict jsonpath that
    -- tests each of audit.SearchableKeys, by its exact spelling, for a string
    -- containing the text. Strict, so a shown key holding an array or an object
    -- (which Redact hides) is an error, which a filter reads as "no", and so a
    -- diff that is itself an array is not searched. Measured against
    -- jsonb_each over every key: about 10 times cheaper on a full scan, and
    -- still the dearest part of a search, so it is bounded: diff_floor is the
    -- created_at of the Nth newest entry (AuditDiffSearchFloor), and older
    -- entries are matched on what is cheap to search. NULL means no bound.
    OR (
      (sqlc.narg(diff_floor)::timestamptz IS NULL OR audit_log.created_at >= sqlc.narg(diff_floor)::timestamptz)
      AND (
        jsonb_path_exists(audit_log.before, sqlc.narg(diff_path)::jsonpath, '{}'::jsonb, true)
        OR jsonb_path_exists(audit_log.after, sqlc.narg(diff_path)::jsonpath, '{}'::jsonb, true)
      )
    )
  )
  AND (sqlc.narg(exclude_actions)::text[] IS NULL OR NOT (action = ANY (sqlc.narg(exclude_actions)::text[])))
-- id breaks the tie, because created_at alone does not order entries written
-- in the same microsecond — which happens inside a single request. Without a
-- total order, two pages of one listing can disagree about which tied row
-- comes first and the same entry appears twice, or not at all.
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SearchAuditLogText :many
-- SearchAuditLog for a search with text (q). The WHERE is the same, kept in step
-- by hand; only the ORDER BY differs. With q the planner cannot tell how few rows
-- match, expects plenty, and walks created_at backwards fetching every row from the
-- heap to find the first page: on 2,000,000 entries a text matching nothing took
-- 8 to 40 seconds (once past a 120 second limit) that way, against about 2 for a
-- parallel scan and a top-N sort. Adding an interval to created_at takes that
-- ordering away from the index, so a search reads the table once and predictably.
-- The listing with no q keeps the index, where stopping after a page is the point.
SELECT * FROM audit_log
WHERE (sqlc.narg(entity_type)::text IS NULL OR entity_type = sqlc.narg(entity_type)::text)
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action)::text)
  AND (sqlc.narg(actor_id)::uuid IS NULL OR actor_id = sqlc.narg(actor_id)::uuid)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR created_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR created_at <= sqlc.narg(to_ts)::timestamptz)
  AND (
    sqlc.narg(q_pattern)::text IS NULL
    OR entity_type ILIKE '%' || sqlc.narg(q_pattern)::text || '%'
    OR action ILIKE '%' || sqlc.narg(q_pattern)::text || '%'
    -- The actor's name and email (#629, D34). Uncorrelated, so it is evaluated
    -- once and probed by actor_id, not once per row. A deleted account is not
    -- named by the view, so it is not matched either; a requester is left out
    -- while the mask is on, so the search is not a way round it.
    OR actor_id IN (
      SELECT u.id FROM users u
      WHERE u.deleted_at IS NULL
        AND (u.display_name ILIKE '%' || sqlc.narg(q_pattern)::text || '%'
             OR u.email ILIKE '%' || sqlc.narg(q_pattern)::text || '%')
        AND NOT (sqlc.arg(mask_requesters)::bool AND u.role = 'user')
    )
    -- The change details (#629, D33), at the keys audit.Redact shows and nowhere
    -- else. diff_path is built by auditstore.diffPath: a strict jsonpath that
    -- tests each of audit.SearchableKeys, by its exact spelling, for a string
    -- containing the text. Strict, so a shown key holding an array or an object
    -- (which Redact hides) is an error, which a filter reads as "no", and so a
    -- diff that is itself an array is not searched. Measured against
    -- jsonb_each over every key: about 10 times cheaper on a full scan, and
    -- still the dearest part of a search, so it is bounded: diff_floor is the
    -- created_at of the Nth newest entry (AuditDiffSearchFloor), and older
    -- entries are matched on what is cheap to search. NULL means no bound.
    OR (
      (sqlc.narg(diff_floor)::timestamptz IS NULL OR audit_log.created_at >= sqlc.narg(diff_floor)::timestamptz)
      AND (
        jsonb_path_exists(audit_log.before, sqlc.narg(diff_path)::jsonpath, '{}'::jsonb, true)
        OR jsonb_path_exists(audit_log.after, sqlc.narg(diff_path)::jsonpath, '{}'::jsonb, true)
      )
    )
  )
  AND (sqlc.narg(exclude_actions)::text[] IS NULL OR NOT (action = ANY (sqlc.narg(exclude_actions)::text[])))
-- id breaks the tie, because created_at alone does not order entries written
-- in the same microsecond — which happens inside a single request. Without a
-- total order, two pages of one listing can disagree about which tied row
-- comes first and the same entry appears twice, or not at all.
ORDER BY created_at + INTERVAL '0 seconds' DESC, id DESC
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
      sqlc.narg(q_pattern)::text IS NULL
      OR entity_type ILIKE '%' || sqlc.narg(q_pattern)::text || '%'
      OR action ILIKE '%' || sqlc.narg(q_pattern)::text || '%'
      -- The actor's name and email (#629, D34). Uncorrelated, so it is evaluated
      -- once and probed by actor_id, not once per row. A deleted account is not
      -- named by the view, so it is not matched either; a requester is left out
      -- while the mask is on, so the search is not a way round it.
      OR actor_id IN (
        SELECT u.id FROM users u
        WHERE u.deleted_at IS NULL
          AND (u.display_name ILIKE '%' || sqlc.narg(q_pattern)::text || '%'
               OR u.email ILIKE '%' || sqlc.narg(q_pattern)::text || '%')
          AND NOT (sqlc.arg(mask_requesters)::bool AND u.role = 'user')
      )
      -- The change details, bounded: see SearchAuditLog.
      OR (
        (sqlc.narg(diff_floor)::timestamptz IS NULL OR audit_log.created_at >= sqlc.narg(diff_floor)::timestamptz)
        AND (
          jsonb_path_exists(audit_log.before, sqlc.narg(diff_path)::jsonpath, '{}'::jsonb, true)
          OR jsonb_path_exists(audit_log.after, sqlc.narg(diff_path)::jsonpath, '{}'::jsonb, true)
        )
      )
    )
    AND (sqlc.narg(exclude_actions)::text[] IS NULL OR NOT (action = ANY (sqlc.narg(exclude_actions)::text[])))
  LIMIT sqlc.arg(count_cap)
) AS matched;

-- name: AuditDiffSearchFloor :one
-- Where a search of the change details stops (#629): the created_at of the
-- window_size'th newest entry that passes the filters other than q. No row
-- means fewer than that many, so nothing is left out. Scoped by the same
-- filters as the search, so narrowing a search (an entity, an actor, a date
-- range) moves the window to reach older entries.
SELECT created_at FROM audit_log
WHERE (sqlc.narg(entity_type)::text IS NULL OR entity_type = sqlc.narg(entity_type)::text)
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action)::text)
  AND (sqlc.narg(actor_id)::uuid IS NULL OR actor_id = sqlc.narg(actor_id)::uuid)
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR created_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR created_at <= sqlc.narg(to_ts)::timestamptz)
  AND (sqlc.narg(exclude_actions)::text[] IS NULL OR NOT (action = ANY (sqlc.narg(exclude_actions)::text[])))
ORDER BY created_at DESC
OFFSET sqlc.arg(window_size)::int - 1
LIMIT 1;

-- name: DeleteAuditLogBatch :execrows
-- The retention sweep's hard delete, one batch (#629). No archive table — see
-- audit.Store's own comment on DeleteOlderThan for why. Oldest first, so the
-- created_at index serves it; SKIP LOCKED so a row someone else holds is left
-- for the next sweep and not waited on.
DELETE FROM audit_log WHERE id IN (
  SELECT old.id FROM audit_log AS old
  WHERE old.created_at < sqlc.arg(cutoff)
  ORDER BY old.created_at
  LIMIT sqlc.arg(batch_size)
  FOR UPDATE SKIP LOCKED
);
