-- The marker is lost; the links stay. Rolled back, every parent_child link out
-- of a closed ticket counts as its follow-up again, as it did before.
ALTER TABLE ticket_links DROP COLUMN follow_up;
