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
    -- still the dearest part of a search, so it runs only when search_details is
    -- true: the caller sets it when the request has a date range of at most
    -- audit.DetailsSearchMaxDays (audit.Filter.SearchesDetails). Otherwise q is
    -- matched on the entity type, the action and the actor alone.
    OR (
      sqlc.arg(search_details)::bool
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
-- heap to find the first page. Adding an interval to created_at takes that
-- ordering away from the index, so a search reads the table once, in parallel.
-- Kept after the date-range rule (audit.DetailsSearchMaxDays) because the range
-- does not remove the need: on 2,000,000 entries over two years, for a text that
-- matches nothing, the page took 0.6 to 2.1 seconds this way and 1.1 to 9.3 the
-- other, whether the search had a 90-day range or none. The price is a text that
-- matches plenty (a first page the index would have found at once): 0.6 to 3.5
-- seconds here against a few milliseconds. The listing with no q keeps the
-- index, where stopping after a page is the point.
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
    -- still the dearest part of a search, so it runs only when search_details is
    -- true: the caller sets it when the request has a date range of at most
    -- audit.DetailsSearchMaxDays (audit.Filter.SearchesDetails). Otherwise q is
    -- matched on the entity type, the action and the actor alone.
    OR (
      sqlc.arg(search_details)::bool
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
      -- The change details, only inside a date range: see SearchAuditLog.
      OR (
        sqlc.arg(search_details)::bool
        AND (
          jsonb_path_exists(audit_log.before, sqlc.narg(diff_path)::jsonpath, '{}'::jsonb, true)
          OR jsonb_path_exists(audit_log.after, sqlc.narg(diff_path)::jsonpath, '{}'::jsonb, true)
        )
      )
    )
    AND (sqlc.narg(exclude_actions)::text[] IS NULL OR NOT (action = ANY (sqlc.narg(exclude_actions)::text[])))
  LIMIT sqlc.arg(count_cap)
) AS matched;

-- name: DeleteAuditLogBatch :execrows
-- The retention sweep's hard delete, one batch (#629). No archive table — see
-- audit.Store's own comment on DeleteOlderThan for why. Oldest first, so the
-- created_at index serves it; SKIP LOCKED so a row someone else holds is left
-- for the next sweep and not waited on.
--
-- The batch is a MATERIALIZED CTE, not `id IN (SELECT ... LIMIT n)`. With the
-- subquery inline, a nested-loop semi join re-runs it for every row of
-- audit_log, and each re-run skips the rows this statement has already deleted
-- and takes the next n: one batch then deletes every expired row, not n. Which
-- plan wins follows the table statistics. The CTE is evaluated once, whatever
-- the plan.
WITH batch AS MATERIALIZED (
  SELECT old.id FROM audit_log AS old
  WHERE old.created_at < sqlc.arg(cutoff)
  ORDER BY old.created_at
  LIMIT sqlc.arg(batch_size)
  FOR UPDATE SKIP LOCKED
)
DELETE FROM audit_log USING batch WHERE audit_log.id = batch.id;
