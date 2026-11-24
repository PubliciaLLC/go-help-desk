-- Make ticket search use an index.
--
-- Every search query has the shape:
--     tracking_number ILIKE $x OR search_vector @@ to_tsquery(...)
--
-- ILIKE cannot use the UNIQUE btree on tracking_number, and because the two
-- halves are joined by OR, Postgres could not use the full-text index for the
-- other half either — it read the whole table. Measured at 200k tickets: 0.13 ms
-- for the full-text half alone, 31-33 ms for the shape actually used, growing in
-- a straight line with the ticket count, on every keystroke of search-as-you-type.
--
-- Making BOTH halves indexable is what fixes it: the planner can then combine
-- two index scans with a BitmapOr instead of scanning. Trigram, not
-- text_pattern_ops, because searchPattern produces a leading-wildcard pattern
-- ('%foo%') for anything that does not look like a tracking number, and a btree
-- cannot serve that.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS tickets_tracking_number_trgm_idx
    ON tickets USING gin (tracking_number gin_trgm_ops);

-- The auto-close sweep reads every resolved ticket that is not yet closed and
-- had no index at all, so it scanned the table on every run. Partial, because
-- closed tickets are the majority over time and never qualify.
CREATE INDEX IF NOT EXISTS tickets_resolved_at_open_idx
    ON tickets (resolved_at) WHERE closed_at IS NULL;

-- Ticket lists order by created_at and filter by category; both were sequential
-- scans with a top-N sort.
CREATE INDEX IF NOT EXISTS tickets_created_at_idx ON tickets (created_at DESC);
CREATE INDEX IF NOT EXISTS tickets_category_id_idx ON tickets (category_id);

-- Staff visibility joins group_members on user_id alone, but the primary key is
-- (group_id, user_id), which cannot serve that lookup. This was the single
-- worst query measured: 105 ms per page for every scoped staff member.
CREATE INDEX IF NOT EXISTS group_members_user_id_idx ON group_members (user_id);

-- These three duplicate the index their UNIQUE constraint already creates. They
-- cost write time and save nothing.
DROP INDEX IF EXISTS users_email_idx;
DROP INDEX IF EXISTS api_keys_hashed_token_idx;
DROP INDEX IF EXISTS tags_name_idx;
