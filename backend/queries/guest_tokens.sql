-- name: CreateGuestAccessToken :exec
INSERT INTO guest_access_tokens (id, ticket_id, token_hash, expires_at, created_at)
VALUES ($1, $2, $3, $4, now());

-- name: GetTicketByGuestToken :one
-- Resolves a raw token's hash to the ticket it names, in one round trip.
--
-- Expiry is decided here rather than in Go so that an expired token behaves
-- exactly like a missing one — the row simply does not load, and there is no
-- branch in the caller that could answer 403 and confirm the token was real.
-- clock_timestamp() rather than now() for the same reason as sessions: now()
-- is the transaction's start time, so two clocks were deciding one lifetime.
--
-- Closed tickets are NOT excluded (#349): closing stops rotating the link
-- instead of revoking it, so the last link sent keeps reading the archive until
-- it expires. Whether a link may WRITE is decided by the caller from the
-- ticket's status (ticket.Service.TicketForGuestWrite), so the one query serves
-- both and the write refusal is the same "not found" as any bad link.
-- The explicit column list, not sqlc.embed: tickets carries a search_vector
-- that no caller wants and that has no Go type worth naming. This is the same
-- list GetTicketByID selects, so both map through one row shape.
SELECT t.id, t.tracking_number, t.subject, t.description, t.category_id, t.type_id,
       t.item_id, t.priority, t.status_id, t.assignee_user_id, t.assignee_group_id,
       t.reporter_user_id, t.guest_email, t.resolution_notes, t.resolved_at,
       t.closed_at, t.created_at, t.updated_at, t.guest_name, t.guest_phone,
       t.pending_since, t.sla_paused_seconds
FROM guest_access_tokens g
JOIN tickets t ON t.id = g.ticket_id
WHERE g.token_hash = $1
  AND g.expires_at > clock_timestamp();

-- name: TouchGuestAccessToken :exec
-- First use stamps the row. Separate from the lookup so a read of the ticket
-- is not also a write on the hot path when the column is already set.
UPDATE guest_access_tokens SET last_used_at = now()
WHERE token_hash = $1 AND last_used_at IS NULL;

-- name: DeleteGuestAccessTokensForTicket :exec
-- Rotation: remove what the ticket has, then insert a replacement in the same
-- transaction. Used only for a ticket that is not Closed — closing no longer
-- revokes (#349), and a Closed ticket's links are added to, never replaced.
DELETE FROM guest_access_tokens WHERE ticket_id = $1;

-- name: GetTicketIDByTrackingAndGuestEmail :one
-- Backs the re-request flow. Matching on both the tracking number and the
-- address means possession of one alone proves nothing, and the caller
-- answers 202 either way so this cannot be used to test whether either
-- exists.
--
-- Closed tickets match (#349): closing no longer revokes access, so a guest may
-- still ask for a link to read their archived ticket. The send-time step adds
-- a read-only link beside the existing ones rather than rotating them.
SELECT id FROM tickets
WHERE tracking_number = $1
  AND guest_email IS NOT NULL
  AND lower(guest_email) = lower($2);
