-- Signups made after the up migration have no password, and the older code
-- would create their accounts with none. They are deleted rather than given
-- one; the person signs up again.
DELETE FROM pending_registrations WHERE password_hash IS NULL;
ALTER TABLE pending_registrations ALTER COLUMN password_hash SET NOT NULL;
