-- name: ListAuditByEntityActions :many
-- ListAuditByEntity narrowed by action: only_actions keeps just those actions,
-- exclude_actions drops them. Either may be NULL for "no narrowing". It is how
-- a ticket's feed keeps follow and unfollow entries (#441) from counting
-- toward its cap, so a flood of them cannot push real history off the feed.
SELECT * FROM audit_log
WHERE entity_type = sqlc.arg(entity_type) AND entity_id = sqlc.arg(entity_id)
  AND (sqlc.narg(only_actions)::text[] IS NULL OR action = ANY (sqlc.narg(only_actions)::text[]))
  AND (sqlc.narg(exclude_actions)::text[] IS NULL OR NOT (action = ANY (sqlc.narg(exclude_actions)::text[])))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);
