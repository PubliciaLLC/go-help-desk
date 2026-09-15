DROP INDEX IF EXISTS tickets_tracking_number_trgm_idx;
DROP INDEX IF EXISTS tickets_resolved_at_open_idx;
DROP INDEX IF EXISTS tickets_created_at_idx;
DROP INDEX IF EXISTS tickets_category_id_idx;
DROP INDEX IF EXISTS group_members_user_id_idx;

-- Recreate the duplicates the up migration dropped, so down is a true inverse.
CREATE INDEX IF NOT EXISTS users_email_idx ON users (email);
CREATE INDEX IF NOT EXISTS api_keys_hashed_token_idx ON api_keys (hashed_token);
CREATE INDEX IF NOT EXISTS tags_name_idx ON tags (name);

-- pg_trgm is left installed: another object may depend on it by now, and
-- dropping an extension is not something a schema rollback should do silently.
