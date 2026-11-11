-- name: NextTicketSeq :one
SELECT nextval('ticket_seq')::bigint;

-- name: CreateTicket :exec
INSERT INTO tickets (
    id, tracking_number, subject, description,
    category_id, type_id, item_id, priority, status_id,
    assignee_user_id, assignee_group_id, reporter_user_id, guest_email,
    guest_name, guest_phone,
    created_at, updated_at, description_format, source_channel
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19);

-- name: GetTicketByID :one
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets WHERE id = $1;

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
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets WHERE id = $1 FOR UPDATE;

-- name: GetTicketByTrackingNumber :one
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets WHERE tracking_number = $1;

-- name: UpdateTicket :exec
UPDATE tickets
SET subject = $2, description = $3, type_id = $4, item_id = $5,
    priority = $6, status_id = $7, assignee_user_id = $8, assignee_group_id = $9,
    resolution_notes = $10, resolved_at = $11, closed_at = $12, updated_at = $13,
    pending_since = $14, sla_paused_seconds = $15, reopen_count = $16, last_reopened_at = $17, paused_seconds_at_last_reopen = $18
WHERE id = $1;

-- name: UpdateTicketCTI :exec
UPDATE tickets
SET category_id = $2, type_id = $3, item_id = $4, updated_at = $5
WHERE id = $1;

-- name: ListTicketsByReporter :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets WHERE reporter_user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: SearchTicketsByReporter :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets
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
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets WHERE assignee_user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: SearchTicketsByAssigneeUser :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets
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
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets WHERE assignee_group_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: SearchTicketsByAssigneeGroup :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets
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
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets WHERE status_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAllTickets :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: SearchAllTickets :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets
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

-- name: ArchivedCTINode :one
-- Which level of a classification being NEWLY picked is archived: 'category',
-- 'type', 'item', or '' when none is. Highest level first, so a ticket aimed at
-- an archived category is told about the category, not the type under it.
--
-- A NULL argument means "not being picked" and is skipped; the caller passes
-- only the levels that differ from what the ticket already holds, because a
-- ticket already sitting in an archived node keeps it (#421). An id that does
-- not exist matches nothing here: CategoryExists and CTIIsCoherent refuse
-- those.
SELECT (CASE
    WHEN sqlc.narg('category_id')::uuid IS NOT NULL
         AND EXISTS (SELECT 1 FROM categories WHERE id = sqlc.narg('category_id') AND NOT active) THEN 'category'
    WHEN sqlc.narg('type_id')::uuid IS NOT NULL
         AND EXISTS (SELECT 1 FROM types WHERE id = sqlc.narg('type_id') AND NOT active) THEN 'type'
    WHEN sqlc.narg('item_id')::uuid IS NOT NULL
         AND EXISTS (SELECT 1 FROM items WHERE id = sqlc.narg('item_id') AND NOT active) THEN 'item'
    ELSE ''
END)::text AS archived_level;

-- name: UserExists :one
-- Whether a live account holds this id. Used for a supplied reporter, which
-- unlike an assignee may be any role — a ticket is filed on behalf of whoever
-- it is about.
SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL);

-- name: IsAssignableUser :one
-- Whether a user can be given a ticket: the account exists, is not deleted,
-- is not disabled, and is staff. A reporting user is not a queue, and neither
-- is a service user (#420): an OAuth client's identity cannot work a ticket,
-- which also keeps it out of round-robin without a check of its own.
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
  AND NOT service_account
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

-- name: LockGroup :one
-- Locks the group row so nothing can be assigned to it until the transaction
-- ends. A ticket's assignee_group_id takes a share lock on the group it names,
-- and this conflicts with that. Without it a ticket assigned between
-- UnassignTicketsForGroup and the delete would lose its group silently, which
-- is the gap this exists to close.
SELECT id FROM groups WHERE id = $1 FOR UPDATE;

-- name: UnassignTicketsForGroup :many
-- Takes every ticket off a group that is about to be deleted, and says which.
-- All of them, resolved and closed too: the foreign key (ON DELETE SET NULL)
-- would clear those anyway, and the audit trail should record it.
UPDATE tickets
SET assignee_group_id = NULL, updated_at = now()
WHERE assignee_group_id = $1
RETURNING id;

-- name: ListUnassignedTickets :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets
WHERE assignee_user_id IS NULL AND assignee_group_id IS NULL
ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: SearchUnassignedTickets :many
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets
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
-- status_id is the Resolved status's id. Without this filter, a row that
-- satisfies resolved_at < $1 but sits in a different status (a legacy row
-- moved off Resolved by old code that cleared status_id without clearing
-- resolved_at, or a Closed ticket with a stale resolved_at) is listed on
-- every sweep, locked, skipped by stillEligible, and listed again forever —
-- see #191.
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets
WHERE resolved_at IS NOT NULL AND resolved_at < $1 AND status_id = $2 AND closed_at IS NULL
ORDER BY resolved_at ASC
LIMIT $3;

-- name: CreateReply :exec
-- author_id is NULL for a reply written by a guest, who has no account. That is
-- the only way it is NULL: every other path passes the acting user.
-- body_format has no column default (migration 000055), so every writer names it.
INSERT INTO ticket_replies (id, ticket_id, author_id, body, internal, notify_customer, created_at, body_format)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

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
                         detected_mime, sha256, virus_name, content_mismatch, uploaded_by_guest)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: CountGuestAttachments :one
-- Only what a guest uploaded: staff and reporter files do not count toward the
-- guest cap (#445).
SELECT count(*) FROM attachments WHERE ticket_id = $1 AND uploaded_by_guest;

-- name: GetAttachmentByID :one
SELECT * FROM attachments WHERE id = $1;

-- name: ListAttachments :many
SELECT * FROM attachments WHERE ticket_id = $1 ORDER BY created_at ASC;

-- name: AnyAttachmentExists :one
SELECT EXISTS (SELECT 1 FROM attachments);

-- name: ListQuarantinedTicketIDs :many
-- Which of these tickets hold a file the scanner held back. One row per ticket
-- however many files, because the ticket list shows a marker, not a count. Uses
-- the attachments ticket_id index; the page is already cut, so $1 is one page.
SELECT DISTINCT ticket_id FROM attachments
WHERE ticket_id = ANY(sqlc.arg(ticket_ids)::uuid[]) AND virus_name IS NOT NULL;

-- name: DeleteAttachment :exec
DELETE FROM attachments WHERE id = $1;

-- name: CreateTicketLink :exec
INSERT INTO ticket_links (source_ticket_id, target_ticket_id, link_type)
VALUES ($1, $2, $3);

-- name: DeleteTicketLink :execrows
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
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets t
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
SELECT id, tracking_number, subject, description, category_id, type_id, item_id, priority, status_id, assignee_user_id, assignee_group_id, reporter_user_id, guest_email, resolution_notes, resolved_at, closed_at, created_at, updated_at, guest_name, guest_phone, pending_since, sla_paused_seconds, reopen_count, last_reopened_at, paused_seconds_at_last_reopen, description_format, source_channel FROM tickets t
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

-- name: ListStaffScopeRows :many
-- The category/type pairs the caller's groups cover, for ListTicketsFiltered's
-- scoped visibility. type_id NULL means the whole category.
SELECT gs.category_id, gs.type_id
FROM group_scopes gs
JOIN group_members gm ON gm.group_id = gs.group_id
WHERE gm.user_id = $1;

-- name: ListTicketsFiltered :many
-- The one list query: every optional filter, the visibility rule and the sort,
-- in one statement, so a caller never chooses among single-purpose queries and
-- then filters in Go. Filtering and paginating in the same statement is what
-- keeps pages full: a page fetched and then filtered returns short pages and
-- skips rows.
--
-- Visibility is three cases, not a patchable special case:
--   unrestricted   — an admin, or staff while scope enforcement is off
--   reporter_only  — a reporting user, who sees only tickets they reported
--   otherwise      — the DESIGN.md staff scope (same predicate as
--                    ListTicketsVisibleToStaff)
-- Every other criterion is a further AND, so a filter can only narrow what the
-- caller may already see. Nothing below widens it.
--
-- Single values are NULL-means-absent and arrays are cardinality(...) = 0, so
-- one prepared statement serves every combination. The store always passes a
-- non-nil array.
--
-- The SLA bucket joins the CURRENT record only and is computed only when the
-- filter or the sort asks for it (the first condition in the lateral is a
-- parameter-only test, so the planner evaluates it once, not per ticket). It is
-- a lateral join and not a correlated subquery because the planner can hash
-- join it: as a subquery, sorting by SLA took 1 s at 200k instead of 0.25 s. It
-- reproduces sla.StatusFor, the live read the list indicator shows, NOT the
-- breach stamps: StatusFor never reads them, and a target met on time keeps
-- its frozen reading. Per target, with elapsed as sla.ElapsedFrom has it
-- (the clock stops at pending_since or at the instant the target was met):
--   a met target with a frozen reading  -> that reading
--   anything else                       -> LEAST(COALESCE(pending_since, at), at)
--                                          - start - pause, with at = the met
--                                          instant, or now while outstanding
-- 0 = a target at or past 100% (red), 1 = otherwise one at or past 80%
-- (amber, exactly target_min * 48 seconds), 2 = everything else. This is the
-- third copy of the formula, after sla.ElapsedFrom and ListSLABreachCandidates;
-- TestListFiltered_SLAAgreesWithStatusFor holds it to the first.
SELECT t.id, t.tracking_number, t.subject, t.description, t.category_id, t.type_id, t.item_id, t.priority, t.status_id, t.assignee_user_id, t.assignee_group_id, t.reporter_user_id, t.guest_email, t.resolution_notes, t.resolved_at, t.closed_at, t.created_at, t.updated_at, t.guest_name, t.guest_phone, t.pending_since, t.sla_paused_seconds, t.reopen_count, t.last_reopened_at, t.paused_seconds_at_last_reopen, t.description_format, t.source_channel
FROM tickets t
LEFT JOIN LATERAL (
  SELECT CASE
           WHEN e.response   >= make_interval(mins => p.response_target_min)
             OR e.resolution >= make_interval(mins => p.resolution_target_min) THEN 0
           WHEN e.response   >= make_interval(secs => (p.response_target_min::bigint   * 48)::double precision)
             OR e.resolution >= make_interval(secs => (p.resolution_target_min::bigint * 48)::double precision) THEN 1
           ELSE 2
         END AS bucket
  FROM sla_records r
  JOIN sla_policies p ON p.id = r.policy_id
  CROSS JOIN LATERAL (
    SELECT
      CASE WHEN r.first_response_at IS NOT NULL AND r.response_elapsed_at_met_seconds IS NOT NULL
           THEN make_interval(secs => r.response_elapsed_at_met_seconds::double precision)
           ELSE LEAST(COALESCE(t.pending_since, COALESCE(r.first_response_at, sqlc.arg(now)::timestamptz)),
                      COALESCE(r.first_response_at, sqlc.arg(now)::timestamptz))
                - (COALESCE(r.started_at, t.created_at)
                   + make_interval(secs => (t.sla_paused_seconds - r.paused_seconds_at_start)::double precision))
      END AS response,
      CASE WHEN r.resolved_at IS NOT NULL AND r.resolution_elapsed_at_met_seconds IS NOT NULL
           THEN make_interval(secs => r.resolution_elapsed_at_met_seconds::double precision)
           ELSE LEAST(COALESCE(t.pending_since, COALESCE(r.resolved_at, sqlc.arg(now)::timestamptz)),
                      COALESCE(r.resolved_at, sqlc.arg(now)::timestamptz))
                - (COALESCE(r.started_at, t.created_at)
                   + make_interval(secs => (t.sla_paused_seconds - r.paused_seconds_at_start)::double precision))
      END AS resolution
  ) e
  WHERE (sqlc.arg(sla)::text <> '' OR sqlc.arg(sort_key)::text = 'sla')
    AND r.ticket_id = t.id
    AND r.superseded_at IS NULL
) sb ON TRUE
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
        -- The caller's groups and the categories and types those groups cover
        -- arrive as plain arrays, read by the store just before this runs, for
        -- the reason given at mine_group_ids below. As subqueries the planner
        -- could not tell a staff member in no group from one in every group,
        -- assumed most tickets match, and walked the whole of tickets_created_at_idx
        -- discarding every row that did not: 1M tickets read 205k buffers and took
        -- 0.9 s for a staff member with 10 assignments, and the cost grew with the
        -- table. As arrays an empty set folds away and each arm that remains can
        -- use an index (reporter, assignee, group, category).
        -- Same answer as ListTicketsVisibleToStaff:
        -- TestListFiltered_ScopedVisibilityMatchesStaffQuery holds them together.
        OR t.assignee_group_id = ANY(sqlc.arg(scope_group_ids)::uuid[])
        OR t.category_id = ANY(sqlc.arg(scope_category_ids)::uuid[])
        -- One type of a category: the type conjunct is what lets an index
        -- (tickets_type_id_idx) serve the arm, and the pair test is the rule.
        -- The conjunct is implied by the pair, so it never changes the answer.
        -- A ticket with no type never matches the row comparison, as it never
        -- matched gs.type_id = t.type_id.
        OR (t.type_id = ANY(sqlc.arg(scope_type_ids)::uuid[])
            AND (t.category_id, t.type_id) IN (SELECT c.id, ty.id
                FROM unnest(sqlc.arg(scope_type_category_ids)::uuid[]) WITH ORDINALITY AS c(id, n)
                JOIN unnest(sqlc.arg(scope_type_ids)::uuid[]) WITH ORDINALITY AS ty(id, n) ON ty.n = c.n))
      )
    )
  )
  -- queue: 'any' | 'mine' | 'unassigned'. Set by the handler, never the caller.
  AND (
    sqlc.arg(queue)::text = 'any'
    OR (sqlc.arg(queue)::text = 'unassigned'
        AND t.assignee_user_id IS NULL AND t.assignee_group_id IS NULL)
    -- mine_group_ids is the caller's groups, read by the store just before this
    -- runs and passed as a plain array. That is deliberate: the planner cannot
    -- know how many groups a subquery will return, assumes a lot, and then walks
    -- the whole created_at index looking for rows a user with few or no
    -- assignments does not have (83-95 ms at 200k tickets for an administrator).
    -- Given the array itself it sees an empty or two-element list, and drives the
    -- queue from the two assignee indexes instead (0.8 ms).
    OR (sqlc.arg(queue)::text = 'mine' AND (
        t.assignee_user_id = sqlc.arg(actor_id)::uuid
        OR t.assignee_group_id = ANY(sqlc.arg(mine_group_ids)::uuid[])))
  )
  AND (cardinality(sqlc.arg(status_ids)::uuid[]) = 0 OR t.status_id = ANY(sqlc.arg(status_ids)::uuid[]))
  -- IS DISTINCT FROM, not <>: if the Closed status were ever missing the
  -- subquery is NULL, and <> would then exclude every ticket.
  AND (NOT sqlc.arg(exclude_closed)::bool
       OR t.status_id IS DISTINCT FROM (SELECT s.id FROM statuses s WHERE s.name = 'Closed' AND s.kind = 'system'))
  AND (cardinality(sqlc.arg(priorities)::text[]) = 0 OR t.priority = ANY(sqlc.arg(priorities)::text[]))
  AND (sqlc.narg(category_id)::uuid IS NULL OR t.category_id = sqlc.narg(category_id)::uuid)
  AND (sqlc.narg(type_id)::uuid IS NULL OR t.type_id = sqlc.narg(type_id)::uuid)
  AND (sqlc.narg(item_id)::uuid IS NULL OR t.item_id = sqlc.narg(item_id)::uuid)
  AND (sqlc.narg(assignee_user_id)::uuid IS NULL OR t.assignee_user_id = sqlc.narg(assignee_user_id)::uuid)
  AND (sqlc.narg(assignee_group_id)::uuid IS NULL OR t.assignee_group_id = sqlc.narg(assignee_group_id)::uuid)
  AND (sqlc.narg(reporter_id)::uuid IS NULL OR t.reporter_user_id = sqlc.narg(reporter_id)::uuid)
  -- requester_pattern is '%' || LIKE-escaped text || '%', built in the store.
  -- It matches the ticket's guest fields and the reporting account's name and
  -- email. Deleted and disabled accounts are included on purpose: their tickets
  -- still exist and the ticket page still names them.
  AND (sqlc.narg(requester_pattern)::text IS NULL
       OR t.guest_name ILIKE sqlc.narg(requester_pattern)::text
       OR t.guest_email ILIKE sqlc.narg(requester_pattern)::text
       -- ARRAY(subquery), not IN (subquery), so all three arms can be indexes
       -- feeding one BitmapOr (see the queue arm above for the same reason).
       OR t.reporter_user_id = ANY(ARRAY(SELECT u.id FROM users u
                                         WHERE u.display_name ILIKE sqlc.narg(requester_pattern)::text
                                            OR u.email ILIKE sqlc.narg(requester_pattern)::text)))
  AND (sqlc.narg(created_after)::timestamptz IS NULL OR t.created_at >= sqlc.narg(created_after)::timestamptz)
  AND (sqlc.narg(created_before)::timestamptz IS NULL OR t.created_at < sqlc.narg(created_before)::timestamptz)
  AND (sqlc.narg(updated_after)::timestamptz IS NULL OR t.updated_at >= sqlc.narg(updated_after)::timestamptz)
  AND (sqlc.narg(updated_before)::timestamptz IS NULL OR t.updated_at < sqlc.narg(updated_before)::timestamptz)
  AND (cardinality(sqlc.arg(source_channels)::text[]) = 0 OR t.source_channel = ANY(sqlc.arg(source_channels)::text[]))
  AND (sqlc.arg(sla)::text = ''
       OR (sqlc.arg(sla)::text = 'breached' AND sb.bucket = 0)
       OR (sqlc.arg(sla)::text = 'at_risk' AND sb.bucket = 1))
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
-- sort_key and sort_dir come from Filter.ResolvedSort, already validated. Each
-- CASE is NULL for every row unless it is the chosen column, so exactly one of
-- them orders the result; with the parameters known the planner folds the rest
-- away. Integer ranks take their direction from a sign instead of a second term.
-- Every sort ends with created_at DESC, id, so equal rows keep a stable order
-- across pages.
ORDER BY
  CASE WHEN sqlc.arg(sort_key)::text = 'relevance' AND sqlc.arg(search_query)::text <> ''
       THEN ts_rank(t.search_vector, to_tsquery('english', sqlc.arg(search_query)::text)) END DESC,
  CASE WHEN sqlc.arg(sort_key)::text = 'number' AND sqlc.arg(sort_dir)::text = 'asc' THEN t.tracking_number END ASC,
  CASE WHEN sqlc.arg(sort_key)::text = 'number' AND sqlc.arg(sort_dir)::text = 'desc' THEN t.tracking_number END DESC,
  CASE WHEN sqlc.arg(sort_key)::text = 'subject' AND sqlc.arg(sort_dir)::text = 'asc' THEN lower(t.subject) END ASC,
  CASE WHEN sqlc.arg(sort_key)::text = 'subject' AND sqlc.arg(sort_dir)::text = 'desc' THEN lower(t.subject) END DESC,
  -- Status order is each status's place in (sort_order, name), looked up in an
  -- array built once per query. A join or a subquery per ticket cost 85 to
  -- 684 ms at 200k when sorting by status, and a join cost every other list too.
  CASE WHEN sqlc.arg(sort_key)::text = 'status'
       THEN array_position((SELECT array_agg(s.id ORDER BY s.sort_order, s.name) FROM statuses s), t.status_id)
            * CASE WHEN sqlc.arg(sort_dir)::text = 'asc' THEN 1 ELSE -1 END END ASC,
  CASE WHEN sqlc.arg(sort_key)::text = 'priority'
       THEN (CASE t.priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END)
            * CASE WHEN sqlc.arg(sort_dir)::text = 'asc' THEN 1 ELSE -1 END END ASC,
  CASE WHEN sqlc.arg(sort_key)::text = 'sla'
       THEN COALESCE(sb.bucket, 2) * CASE WHEN sqlc.arg(sort_dir)::text = 'asc' THEN 1 ELSE -1 END END ASC,
  CASE WHEN sqlc.arg(sort_key)::text = 'created' AND sqlc.arg(sort_dir)::text = 'asc' THEN t.created_at END ASC,
  CASE WHEN sqlc.arg(sort_key)::text = 'created' AND sqlc.arg(sort_dir)::text = 'desc' THEN t.created_at END DESC,
  CASE WHEN sqlc.arg(sort_key)::text = 'updated' AND sqlc.arg(sort_dir)::text = 'asc' THEN t.updated_at END ASC,
  CASE WHEN sqlc.arg(sort_key)::text = 'updated' AND sqlc.arg(sort_dir)::text = 'desc' THEN t.updated_at END DESC,
  t.created_at DESC,
  t.id
LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);
