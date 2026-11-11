-- Which parent_child links were written by the follow-up action (#625).
--
-- A requester gets one follow-up per closed ticket, and "the follow-up" used to
-- be ANY parent_child link out of the closed ticket, because the row said
-- nothing about who made it: a staff member who linked a closed ticket as the
-- parent of another used up the requester's one follow-up. The flag lets the
-- cap count only what the follow-up action made.
--
-- Every parent_child link that exists now is backfilled TRUE, so what counted
-- before still counts: the old rows cannot be told apart, and treating them as
-- hand-made would let a requester open a second follow-up on every ticket that
-- already has one. Only links made after this migration can be hand-made and
-- not count.
--
-- ADD COLUMN with a constant default is a catalogue change, not a table
-- rewrite. The UPDATE rewrites the parent_child rows only.
ALTER TABLE ticket_links ADD COLUMN follow_up BOOLEAN NOT NULL DEFAULT false;

UPDATE ticket_links SET follow_up = true WHERE link_type = 'parent_child';
