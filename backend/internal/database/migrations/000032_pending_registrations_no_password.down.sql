-- Deletes EVERY pending signup, not only those made after the up migration:
-- the up migration cleared every stored hash, so no row has one. The older
-- code would create accounts with no password from them. Anyone with a
-- pending signup at rollback signs up again.
DELETE FROM pending_registrations WHERE password_hash IS NULL;
ALTER TABLE pending_registrations ALTER COLUMN password_hash SET NOT NULL;
