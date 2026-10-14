-- Set by the server console command go-help-desk unlock (#497): the password it
-- printed is known to whoever ran it, so the next sign-in may only change it.
-- Cleared when the account holder sets a password of their own.
ALTER TABLE users ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT false;
