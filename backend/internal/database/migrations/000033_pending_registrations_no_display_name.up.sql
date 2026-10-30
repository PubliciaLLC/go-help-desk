-- #374: the display name is chosen on the verification page, beside the
-- password (#360), so a second signup for the same address can no longer set
-- the name the owner's account gets. The column is no longer written; it is
-- dropped in a later migration with password_hash, per CLAUDE.md
-- ("deprecate, then remove in a separate commit").
--
-- Names already stored are cleared: nothing reads them any more, and any of
-- them may have been set by somebody other than the inbox's owner.
ALTER TABLE pending_registrations ALTER COLUMN display_name DROP NOT NULL;
UPDATE pending_registrations SET display_name = NULL;
