ALTER TABLE users
    DROP COLUMN mfa_failed_attempts,
    DROP COLUMN mfa_locked_until;
