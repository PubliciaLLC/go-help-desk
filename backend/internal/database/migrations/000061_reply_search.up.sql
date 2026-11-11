-- Reply-text search (advanced ticket search, PR R). The search queries match
-- to_tsvector('english', body) @@ to_tsquery(...) on ticket_replies, and this
-- is the index they use.
--
-- An expression index, not a stored generated tsvector column like
-- tickets.search_vector. A stored column rewrites the whole table under an
-- ACCESS EXCLUSIVE lock and roughly doubles the disk the reply text takes; an
-- index gives the same @@ lookups with neither. The queries must spell the
-- expression exactly as it is spelled here, or the planner will not use the
-- index (a store test checks the plan).
--
-- This runs at server start, before the server takes requests, so a plain
-- CREATE INDEX (SHARE lock: reads continue, writes wait) costs nothing extra
-- over a concurrent build, which would read the table twice. CREATE INDEX
-- CONCURRENTLY is not usable here anyway: golang-migrate sends this file as one
-- multi-statement execution, which Postgres runs as one transaction.
--
-- An operator with a large reply table can build the index while 1.2.x is
-- still serving:
--
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS ticket_replies_body_fts_idx
--       ON ticket_replies USING gin (to_tsvector('english', body));
--
-- and the CREATE INDEX IF NOT EXISTS below then finds it and does nothing.
-- docs/upgrade-notes-1.3.0.md says so.
--
-- A concurrent build that fails partway leaves an INVALID index under this
-- name. IF NOT EXISTS would skip it without a word and every reply search
-- would scan the table, so an invalid one is dropped first. to_regclass
-- resolves the name on the search path, as the CREATE INDEX does.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index
               WHERE indexrelid = to_regclass('ticket_replies_body_fts_idx')
                 AND NOT indisvalid) THEN
        DROP INDEX ticket_replies_body_fts_idx;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS ticket_replies_body_fts_idx
    ON ticket_replies USING gin (to_tsvector('english', body));
