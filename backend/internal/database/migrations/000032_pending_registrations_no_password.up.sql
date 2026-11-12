-- #360: the password is chosen on the verification page, not at signup, so a
-- second signup for the same address can no longer replace it. The column is
-- no longer written; it is dropped in a later migration, per CLAUDE.md
-- ("deprecate, then remove in a separate commit").
--
-- Hashes already stored are cleared: nothing reads them any more, and a
-- password nobody will use is not worth keeping.
ALTER TABLE pending_registrations ALTER COLUMN password_hash DROP NOT NULL;
UPDATE pending_registrations SET password_hash = NULL;
