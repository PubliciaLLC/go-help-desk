-- name: AddTicketFollower :execrows
-- Rows affected is 1 for a new follower and 0 for one already following, which
-- is how the service tells a change worth an audit entry from a repeat.
INSERT INTO ticket_followers (ticket_id, user_id) VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RemoveTicketFollower :execrows
DELETE FROM ticket_followers WHERE ticket_id = $1 AND user_id = $2;

-- name: ListTicketFollowers :many
-- The list staff see on the ticket. Soft-deleted accounts are left out: there
-- is nobody to show. A disabled account is shown, because the row is still
-- there and following resumes if it is re-enabled.
SELECT f.user_id, u.display_name, f.created_at
FROM ticket_followers f
JOIN users u ON u.id = f.user_id
WHERE f.ticket_id = $1 AND u.deleted_at IS NULL
ORDER BY f.created_at, f.user_id;

-- name: ListMailableTicketFollowers :many
-- Followers staff email can go to (#441): active staff and administrators,
-- never a service account, a disabled or deleted account, or somebody moved
-- to a requester role. Whether they can still SEE the ticket is not a
-- question for SQL; the send-time check asks it (Server.StillEntitled).
SELECT f.user_id
FROM ticket_followers f
JOIN users u ON u.id = f.user_id
WHERE f.ticket_id = $1
  AND u.deleted_at IS NULL
  AND NOT u.disabled
  AND NOT u.service_account
  AND u.role IN ('staff', 'admin')
ORDER BY f.user_id;
