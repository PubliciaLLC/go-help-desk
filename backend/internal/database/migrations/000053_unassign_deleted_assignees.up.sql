-- Open tickets held by a user deleted under 1.2.x go back to the queue (#524).
--
-- Deleting a user is a soft delete. 1.2.x left the assignee column pointing at
-- the deleted row, so the ticket showed as "Unassigned" on the page, was not in
-- the unassigned queue (the column was not null) and was in nobody's "assigned
-- to me". 1.3.0's delete returns the user's open tickets to the queue itself
-- (ticket.Service.UnassignForUser); this does the same for the ones a 1.2.x
-- delete stranded, once.
--
-- Same rule: open tickets only (neither resolved nor closed). A resolved or
-- closed ticket is history and keeps whoever handled it. A disabled user is
-- still in the user list, so their tickets are not stranded and are left alone.
--
-- Same audit entry as UnassignForUser: action "unassigned" on the ticket, with
-- the assignee before and after. It has no actor, because no person is behind a
-- migration. The trail would otherwise read "assigned to Ann" and then show no
-- assignee with nothing to say why.
--
-- No follower handling: ticket_followers is created by 000046, after 1.2.x, so
-- for a database coming from 1.2.x it is empty when this runs. A database that
-- ran a 1.3.0 beta has had 1.3.0's delete, which already ended those follows.
--
-- A group needs nothing: tickets.assignee_group_id is ON DELETE SET NULL, so a
-- deleted group never left a ticket pointing at nothing.
WITH moved AS (
    UPDATE tickets t
    SET assignee_user_id = NULL, updated_at = now()
    FROM users u
    WHERE u.id = t.assignee_user_id
      AND u.deleted_at IS NOT NULL
      AND t.resolved_at IS NULL
      AND t.closed_at IS NULL
    RETURNING t.id AS ticket_id, u.id AS user_id
)
INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
SELECT NULL, 'ticket', ticket_id, 'unassigned',
       jsonb_build_object('assignee_user_id', user_id::text),
       jsonb_build_object('assignee_user_id', NULL)
FROM moved;
