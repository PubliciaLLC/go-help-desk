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
SELECT * FROM tickets WHERE id = $1;

-- name: GetTicketByTrackingNumber :one
SELECT * FROM tickets WHERE tracking_number = $1;

-- name: UpdateTicket :exec
UPDATE tickets
SET subject = $2, description = $3, type_id = $4, item_id = $5,
    priority = $6, status_id = $7, assignee_user_id = $8, assignee_group_id = $9,
    resolution_notes = $10, resolved_at = $11, closed_at = $12, updated_at = $13
WHERE id = $1;

-- name: ListTicketsByReporter :many
SELECT * FROM tickets WHERE reporter_user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: SearchTicketsByReporter :many
SELECT * FROM tickets
WHERE reporter_user_id = $1
  AND (tracking_number ILIKE $4 OR subject ILIKE $4 OR description ILIKE $4)
ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListTicketsByAssigneeUser :many
SELECT * FROM tickets WHERE assignee_user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: SearchTicketsByAssigneeUser :many
SELECT * FROM tickets
WHERE assignee_user_id = $1
  AND (tracking_number ILIKE $4 OR subject ILIKE $4 OR description ILIKE $4)
ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListTicketsByAssigneeGroup :many
SELECT * FROM tickets WHERE assignee_group_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: SearchTicketsByAssigneeGroup :many
SELECT * FROM tickets
WHERE assignee_group_id = $1
  AND (tracking_number ILIKE $4 OR subject ILIKE $4 OR description ILIKE $4)
ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListTicketsByStatus :many
SELECT * FROM tickets WHERE status_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListResolvedTicketsBefore :many
SELECT * FROM tickets
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
