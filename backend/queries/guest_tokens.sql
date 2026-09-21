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
-- Closed tickets are excluded: closing revokes access, and doing it in the
-- query means a token that outlived its DELETE by a moment still reaches
-- nothing.
-- The explicit column list, not sqlc.embed: tickets carries a search_vector
-- that no caller wants and that has no Go type worth naming. This is the same
-- list GetTicketByID selects, so both map through one row shape.
SELECT t.id, t.tracking_number, t.subject, t.description, t.category_id, t.type_id,
       t.item_id, t.priority, t.status_id, t.assignee_user_id, t.assignee_group_id,
       t.reporter_user_id, t.guest_email, t.resolution_notes, t.resolved_at,
       t.closed_at, t.created_at, t.updated_at, t.guest_name, t.guest_phone
FROM guest_access_tokens g
JOIN tickets t ON t.id = g.ticket_id
WHERE g.token_hash = $1
  AND g.expires_at > clock_timestamp()
  AND t.closed_at IS NULL;

-- name: TouchGuestAccessToken :exec
-- First use stamps the row. Separate from the lookup so a read of the ticket
-- is not also a write on the hot path when the column is already set.
UPDATE guest_access_tokens SET last_used_at = now()
WHERE token_hash = $1 AND last_used_at IS NULL;

-- name: DeleteGuestAccessTokensForTicket :exec
-- Rotation and revocation are the same operation: remove what the ticket has.
-- Rotation then inserts a replacement in the same transaction; revocation
-- does not.
DELETE FROM guest_access_tokens WHERE ticket_id = $1;

-- name: DeleteExpiredGuestAccessTokens :exec
DELETE FROM guest_access_tokens WHERE expires_at <= clock_timestamp();

-- name: GetTicketIDByTrackingAndGuestEmail :one
-- Backs the re-request flow. Matching on both the tracking number and the
-- address means possession of one alone proves nothing, and the caller
-- answers 202 either way so this cannot be used to test whether either
-- exists.
--
-- Closed tickets are excluded so a re-request cannot resurrect access that
-- closing revoked.
SELECT id FROM tickets
WHERE tracking_number = $1
  AND guest_email IS NOT NULL
  AND lower(guest_email) = lower($2)
  AND closed_at IS NULL;
