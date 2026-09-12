-- name: NextTicketSeq :one
SELECT nextval('ticket_seq')::bigint;

-- name: CreateTicket :exec
INSERT INTO tickets (
    id, tracking_number, subject, description,
    category_id, type_id, item_id, priority, status_id,
    assignee_user_id, assignee_group_id, reporter_user_id, guest_email,
    guest_name, guest_phone,
    created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17);

-- name: GetTicketByID :one
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets WHERE id = $1;

-- name: GetTicketByTrackingNumber :one
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets WHERE tracking_number = $1;

-- name: UpdateTicket :exec
UPDATE tickets
SET subject = $2, description = $3, type_id = $4, item_id = $5,
    priority = $6, status_id = $7, assignee_user_id = $8, assignee_group_id = $9,
    resolution_notes = $10, resolved_at = $11, closed_at = $12, updated_at = $13
WHERE id = $1;

-- name: UpdateTicketCTI :exec
UPDATE tickets
SET category_id = $2, type_id = $3, item_id = $4, updated_at = $5
WHERE id = $1;

-- name: ListTicketsByReporter :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets WHERE reporter_user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: SearchTicketsByReporter :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets
WHERE reporter_user_id = $1
  AND (
    tracking_number ILIKE $4
    OR (CASE WHEN sqlc.arg(search_query)::text <> '' THEN search_vector @@ to_tsquery('english', sqlc.arg(search_query)::text) ELSE false END)
  )
ORDER BY
  CASE WHEN sqlc.arg(search_query)::text <> '' THEN ts_rank(search_vector, to_tsquery('english', sqlc.arg(search_query)::text)) ELSE 0 END DESC,
  created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListTicketsByAssigneeUser :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets WHERE assignee_user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: SearchTicketsByAssigneeUser :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets
WHERE assignee_user_id = $1
  AND (
    tracking_number ILIKE $4
    OR (CASE WHEN sqlc.arg(search_query)::text <> '' THEN search_vector @@ to_tsquery('english', sqlc.arg(search_query)::text) ELSE false END)
  )
ORDER BY
  CASE WHEN sqlc.arg(search_query)::text <> '' THEN ts_rank(search_vector, to_tsquery('english', sqlc.arg(search_query)::text)) ELSE 0 END DESC,
  created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListTicketsByAssigneeGroup :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets WHERE assignee_group_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: SearchTicketsByAssigneeGroup :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets
WHERE assignee_group_id = $1
  AND (
    tracking_number ILIKE $4
    OR (CASE WHEN sqlc.arg(search_query)::text <> '' THEN search_vector @@ to_tsquery('english', sqlc.arg(search_query)::text) ELSE false END)
  )
ORDER BY
  CASE WHEN sqlc.arg(search_query)::text <> '' THEN ts_rank(search_vector, to_tsquery('english', sqlc.arg(search_query)::text)) ELSE 0 END DESC,
  created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListTicketsByStatus :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets WHERE status_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAllTickets :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: SearchAllTickets :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets
WHERE (
    tracking_number ILIKE $3
    OR (CASE WHEN sqlc.arg(search_query)::text <> '' THEN search_vector @@ to_tsquery('english', sqlc.arg(search_query)::text) ELSE false END)
  )
ORDER BY
  CASE WHEN sqlc.arg(search_query)::text <> '' THEN ts_rank(search_vector, to_tsquery('english', sqlc.arg(search_query)::text)) ELSE 0 END DESC,
  created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListUnassignedTickets :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets
WHERE assignee_user_id IS NULL AND assignee_group_id IS NULL
ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: SearchUnassignedTickets :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets
WHERE assignee_user_id IS NULL AND assignee_group_id IS NULL
  AND (
    tracking_number ILIKE $3
    OR (CASE WHEN sqlc.arg(search_query)::text <> '' THEN search_vector @@ to_tsquery('english', sqlc.arg(search_query)::text) ELSE false END)
  )
ORDER BY
  CASE WHEN sqlc.arg(search_query)::text <> '' THEN ts_rank(search_vector, to_tsquery('english', sqlc.arg(search_query)::text)) ELSE 0 END DESC,
  created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListResolvedTicketsBefore :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets
WHERE resolved_at IS NOT NULL AND resolved_at < $1 AND closed_at IS NULL
ORDER BY resolved_at ASC
LIMIT $2;

-- name: CreateReply :exec
INSERT INTO ticket_replies (id, ticket_id, author_id, guest_token, body, internal, notify_customer, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListReplies :many
SELECT * FROM ticket_replies WHERE ticket_id = $1 ORDER BY created_at ASC;

-- name: CreateAttachment :exec
INSERT INTO attachments (id, ticket_id, filename, mime_type, size_bytes, storage_path, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetAttachmentByID :one
SELECT * FROM attachments WHERE id = $1;

-- name: ListAttachments :many
SELECT * FROM attachments WHERE ticket_id = $1 ORDER BY created_at ASC;

-- name: DeleteAttachment :exec
DELETE FROM attachments WHERE id = $1;

-- name: CreateTicketLink :exec
INSERT INTO ticket_links (source_ticket_id, target_ticket_id, link_type)
VALUES ($1, $2, $3);

-- name: DeleteTicketLink :exec
DELETE FROM ticket_links
WHERE source_ticket_id = $1 AND target_ticket_id = $2 AND link_type = $3;

-- name: ListTicketLinks :many
SELECT * FROM ticket_links
WHERE source_ticket_id = $1 OR target_ticket_id = $1;

-- name: ListTicketsVisibleToStaff :many
-- Every ticket a staff member may see under DESIGN.md's scope model: reported
-- by them, assigned to them, assigned to one of their groups, or falling in a
-- Category/Type their groups cover. A NULL group_scopes.type_id is a
-- category-level scope covering every type beneath it; items never factor in.
--
-- Filtering happens here rather than in Go because the caller paginates: a page
-- fetched and then filtered returns short pages and skips rows.
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets t
WHERE
  t.reporter_user_id = $1
  OR t.assignee_user_id = $1
  OR t.assignee_group_id IN (SELECT gm.group_id FROM group_members gm WHERE gm.user_id = $1)
  OR EXISTS (
    SELECT 1 FROM group_scopes gs
    JOIN group_members gm ON gm.group_id = gs.group_id
    WHERE gm.user_id = $1
      AND gs.category_id = t.category_id
      AND (gs.type_id IS NULL OR gs.type_id = t.type_id)
  )
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: SearchTicketsVisibleToStaff :many
-- Search variant of ListTicketsVisibleToStaff, matching the predicate and
-- ranking used by the other ticket searches.
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets t
WHERE
  (
    t.reporter_user_id = $1
    OR t.assignee_user_id = $1
    OR t.assignee_group_id IN (SELECT gm.group_id FROM group_members gm WHERE gm.user_id = $1)
    OR EXISTS (
      SELECT 1 FROM group_scopes gs
      JOIN group_members gm ON gm.group_id = gs.group_id
      WHERE gm.user_id = $1
        AND gs.category_id = t.category_id
        AND (gs.type_id IS NULL OR gs.type_id = t.type_id)
    )
  )
  AND (
    tracking_number ILIKE $4
    OR (CASE WHEN sqlc.arg(search_query)::text <> '' THEN search_vector @@ to_tsquery('english', sqlc.arg(search_query)::text) ELSE false END)
  )
ORDER BY
  CASE WHEN sqlc.arg(search_query)::text <> '' THEN ts_rank(search_vector, to_tsquery('english', sqlc.arg(search_query)::text)) ELSE 0 END DESC,
  created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListTicketsFiltered :many
-- The MCP list surface: one query carrying every optional filter plus the
-- visibility rule, rather than the caller choosing among the eight
-- single-purpose list/search queries above and then filtering in Go.
--
-- Visibility is three cases, not a patchable special case:
--   unrestricted   — an admin, or staff while scope enforcement is off
--   reporter_only  — a reporting user, who sees only tickets they reported
--   otherwise      — the DESIGN.md staff scope (same predicate as
--                    ListTicketsVisibleToStaff)
--
-- Every other filter is NULL-means-absent, so one prepared statement serves
-- all combinations. Filtering and paginating in the same statement is what
-- keeps pages full: a page fetched and then filtered returns short pages and
-- skips rows.
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets t
WHERE
  (
    sqlc.arg(unrestricted)::bool
    OR (
      sqlc.arg(reporter_only)::bool
      AND t.reporter_user_id = sqlc.arg(actor_id)::uuid
    )
    OR (
      NOT sqlc.arg(unrestricted)::bool
      AND NOT sqlc.arg(reporter_only)::bool
      AND (
        t.reporter_user_id = sqlc.arg(actor_id)::uuid
        OR t.assignee_user_id = sqlc.arg(actor_id)::uuid
        OR t.assignee_group_id IN (SELECT gm.group_id FROM group_members gm WHERE gm.user_id = sqlc.arg(actor_id)::uuid)
        OR EXISTS (
          SELECT 1 FROM group_scopes gs
          JOIN group_members gm ON gm.group_id = gs.group_id
          WHERE gm.user_id = sqlc.arg(actor_id)::uuid
            AND gs.category_id = t.category_id
            AND (gs.type_id IS NULL OR gs.type_id = t.type_id)
        )
      )
    )
  )
  AND (sqlc.narg(status_id)::uuid IS NULL OR t.status_id = sqlc.narg(status_id)::uuid)
  AND (sqlc.narg(priority)::text IS NULL OR t.priority = sqlc.narg(priority)::text)
  AND (sqlc.narg(category_id)::uuid IS NULL OR t.category_id = sqlc.narg(category_id)::uuid)
  AND (sqlc.narg(assignee_user_id)::uuid IS NULL OR t.assignee_user_id = sqlc.narg(assignee_user_id)::uuid)
  AND (
    sqlc.arg(search_query)::text = ''
    OR t.tracking_number ILIKE sqlc.arg(tracking_pattern)::text
    OR t.search_vector @@ to_tsquery('english', sqlc.arg(search_query)::text)
  )
ORDER BY
  CASE WHEN sqlc.arg(search_query)::text <> '' THEN ts_rank(t.search_vector, to_tsquery('english', sqlc.arg(search_query)::text)) ELSE 0 END DESC,
  t.created_at DESC
LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);
