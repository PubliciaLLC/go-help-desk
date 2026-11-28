-- Deletes every pending signup: the up migration cleared every name, and the
-- older code needs one to create the account. Anyone with a pending signup at
-- rollback signs up again.
DELETE FROM pending_registrations WHERE display_name IS NULL;
ALTER TABLE pending_registrations ALTER COLUMN display_name SET NOT NULL;
