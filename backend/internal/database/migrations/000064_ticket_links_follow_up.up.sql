-- Which parent_child links were written by the follow-up action (#625).
--
-- A requester gets one follow-up per closed ticket, and "the follow-up" used to
-- be ANY parent_child link out of the closed ticket, because the row said
-- nothing about who made it: a staff member who linked a closed ticket as the
-- parent of another used up the requester's one follow-up. The flag lets the
-- cap count only what the follow-up action made.
--
-- Existing links are backfilled TRUE only where the child belongs to the same
-- requester as the parent: the same reporting account, or the same guest
-- address. That is what a follow-up always is (it copies its requester), so
-- every real follow-up keeps counting. The old rows cannot otherwise be told
-- apart from links made by hand, and a link between two different people's
-- tickets is not a follow-up: flagged, it would name the other person's ticket
-- on the closed ticket's page. A requester whose one follow-up was refused only
-- because of such a link can open one now; that is the bug being fixed.
--
-- ADD COLUMN with a constant default is a catalogue change, not a table
-- rewrite. The UPDATE rewrites the matching parent_child rows only.
ALTER TABLE ticket_links ADD COLUMN follow_up BOOLEAN NOT NULL DEFAULT false;

UPDATE ticket_links l
SET follow_up = true
FROM tickets parent, tickets child
WHERE l.link_type = 'parent_child'
  AND parent.id = l.source_ticket_id
  AND child.id = l.target_ticket_id
  AND (
        (parent.reporter_user_id IS NOT NULL AND parent.reporter_user_id = child.reporter_user_id)
     OR (parent.guest_email IS NOT NULL AND lower(parent.guest_email) = lower(child.guest_email))
  );
