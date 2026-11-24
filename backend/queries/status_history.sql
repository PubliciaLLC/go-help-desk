-- name: CreateStatusHistoryEntry :exec
INSERT INTO ticket_status_history (id, ticket_id, from_status_id, to_status_id, changed_by_user_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListTicketStatusHistory :many
SELECT
    h.id,
    h.ticket_id,
    h.from_status_id,
    COALESCE(s_from.name,  '') AS from_status_name,
    COALESCE(s_from.color, '') AS from_status_color,
    h.to_status_id,
    s_to.name                  AS to_status_name,
    s_to.color                 AS to_status_color,
    h.changed_by_user_id,
    COALESCE(u.display_name, '') AS changed_by_name,
    h.created_at
FROM ticket_status_history h
LEFT JOIN statuses s_from ON h.from_status_id  = s_from.id
JOIN      statuses s_to   ON h.to_status_id    = s_to.id
LEFT JOIN users    u      ON h.changed_by_user_id = u.id
WHERE h.ticket_id = $1
ORDER BY h.created_at ASC;
