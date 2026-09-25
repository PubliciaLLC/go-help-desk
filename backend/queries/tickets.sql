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

-- name: GetTicketByIDForUpdate :one
-- The same row as GetTicketByID, with a write lock held until the transaction
-- ends.
--
-- Every lifecycle write used to read the ticket on the pool, mutate the whole
-- struct, and then UPDATE all of it inside a transaction. UpdateTicket is a
-- full-row overwrite, so two staff acting within a few milliseconds silently
-- lost one of the changes — and worse, the history and audit rows for the lost
-- change were still committed, so the ticket contradicted its own timeline: the
-- row said open while ticket_status_history said Resolved.
--
-- Reading here instead serialises the writers. The second one sees the first's
-- committed state and applies its change on top, which is what someone clicking
-- Resolve a moment after someone else clicked Assign expects.
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone FROM tickets WHERE id = $1 FOR UPDATE;

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

-- name: CategoryExists :one
-- Whether a category id is real. Checked before a tracking number is taken,
-- because the foreign key only speaks at the INSERT — by which point the
-- number is gone and the sequence has a permanent gap. Staff and MCP were
-- never validated here; only a reporting user's category was checked, and
-- that check is about whether the category is OPEN to them, not whether it
-- exists.
SELECT EXISTS (SELECT 1 FROM categories WHERE id = $1);

-- name: CTIIsCoherent :one
-- Whether a category/type/item triple exists and hangs together: the type
-- belongs to the category, and the item belongs to the type.
--
-- One question rather than three, because the foreign keys are the only thing
-- that was asking and they speak at the INSERT — after the tracking number
-- has been taken. Verified: five refused creates advanced the sequence by
-- five. REST checked that the type belonged to the category; MCP checked
-- neither; nobody checked the item at all, and there is no composite key for
-- item-to-type, so a ticket could carry a type and an item that do not go
-- together and then be routed on that.
SELECT
    (sqlc.narg('type_id')::uuid IS NULL OR EXISTS (
        SELECT 1 FROM types ty
        WHERE ty.id = sqlc.narg('type_id') AND ty.category_id = sqlc.arg('category_id')))
    AND
    (sqlc.narg('item_id')::uuid IS NULL OR EXISTS (
        SELECT 1 FROM items it
        JOIN types t2 ON t2.id = it.type_id
        WHERE it.id = sqlc.narg('item_id')
          AND it.type_id = sqlc.narg('type_id')
          AND t2.category_id = sqlc.arg('category_id')));

-- name: UserExists :one
-- Whether a live account holds this id. Used for a supplied reporter, which
-- unlike an assignee may be any role — a ticket is filed on behalf of whoever
-- it is about.
SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL);

-- name: IsAssignableUser :one
-- Whether a user can be given a ticket: the account exists, is not deleted,
-- is not disabled, and is staff. A reporting user is not a queue.
--
-- Asked inside the assignment transaction rather than by the caller, because
-- the caller is not the only caller. The REST handler checked this and MCP
-- did not, so `assign_ticket` happily put tickets on deleted accounts and on
-- reporting users — and a check the caller makes is a check every future
-- caller has to remember to make. This one is where the write is.
-- FOR SHARE, so a delete cannot land between this check and the write.
--
-- A plain read let them interleave: the check passed, a concurrent request
-- soft-deleted the account and unassigned its tickets (finding none, because
-- this one was not written yet), and then this transaction committed the
-- assignment — leaving the ticket on a deleted account, which is the limbo
-- the unassign-on-delete work exists to prevent. The share lock makes the
-- delete wait for this transaction instead.
SELECT id FROM users
WHERE id = $1
  AND deleted_at IS NULL
  AND disabled = FALSE
  AND role IN ('staff', 'admin')
FOR SHARE;

-- name: IsAssignableGroup :one
-- Whether a group can be given a ticket. An unknown id used to reach the
-- foreign key and answer 500 for what is a caller's typo.
SELECT EXISTS (SELECT 1 FROM groups WHERE id = $1);

-- name: UnassignTicketsForUser :many
-- Takes a departing user off every ticket still assigned to them, and says
-- which ones.
--
-- Deleting a user is a soft delete, so the assignee column kept pointing at a
-- row that no longer appears anywhere: the ticket showed as "Unassigned" on
-- the page (the lookup found nobody), was NOT in the unassigned queue (the
-- column was not null), and was in nobody's "assigned to me". It sat in the
-- gap between the two lists with nothing to prompt anyone to pick it up.
--
-- Only tickets that are still open are worth moving. A resolved or closed
-- ticket assigned to somebody who has left is history, and history should
-- record who actually handled it.
UPDATE tickets
SET assignee_user_id = NULL, updated_at = now()
WHERE assignee_user_id = $1
  AND resolved_at IS NULL
  AND closed_at IS NULL
RETURNING id;

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
-- author_id is NULL for a reply written by a guest, who has no account. That is
-- the only way it is NULL: every other path passes the acting user.
INSERT INTO ticket_replies (id, ticket_id, author_id, body, internal, notify_customer, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListReplies :many
-- The author's display name comes back with the reply.
--
-- Without it the ticket page had nothing but author_id to render, and rendered
-- it: every reply from a registered account showed as a bare UUID, so a staff
-- member reading a thread could not tell who had said what. A join here rather
-- than a lookup in the browser, because the page cannot do the lookup for a
-- reporting user -- it is not allowed to list users, and should not be.
--
-- LEFT JOIN: author_id is NULL for a guest's reply, which is the one case
-- where there is genuinely no account behind the message.
SELECT r.*, u.display_name AS author_display_name
FROM ticket_replies r
LEFT JOIN users u ON u.id = r.author_id
WHERE r.ticket_id = $1
ORDER BY r.created_at ASC;

-- name: CreateAttachment :exec
INSERT INTO attachments (id, ticket_id, filename, mime_type, size_bytes, storage_path, created_at,
                         detected_mime, sha256, virus_name, content_mismatch)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

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
  -- `searching` says whether the caller asked for a search at all, which is
  -- NOT the same as search_query being empty. A term like "???" contains no
  -- indexable tokens, so buildSearchTSQuery yields "" — and keying off that
  -- alone made the whole clause vanish and returned every visible ticket as
  -- though each one matched. A search that tokenises to nothing must match on
  -- the tracking number or not at all.
  AND (
    NOT sqlc.arg(searching)::bool
    OR t.tracking_number ILIKE sqlc.arg(tracking_pattern)::text
    OR (
      sqlc.arg(search_query)::text <> ''
      AND t.search_vector @@ to_tsquery('english', sqlc.arg(search_query)::text)
    )
  )
ORDER BY
  CASE WHEN sqlc.arg(search_query)::text <> '' THEN ts_rank(t.search_vector, to_tsquery('english', sqlc.arg(search_query)::text)) ELSE 0 END DESC,
  t.created_at DESC
LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);
