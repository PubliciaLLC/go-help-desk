-- Reply-text search (advanced ticket search, PR R). The search matches
-- ticket_replies.body_tsv, a stored tsvector of the reply text, through a GIN
-- index.
--
-- This migration only adds the column and the trigger that keeps it current.
-- It does NOT fill the existing rows and does NOT build the index, on purpose:
--
--   * ADD COLUMN with no default and no NOT NULL is a catalogue change. It
--     rewrites nothing, so it is instant however many replies there are.
--   * Filling a million rows and indexing them takes minutes to hours, and the
--     server migrates before it takes requests. The server fills the existing
--     rows in small batches in the background after it has started
--     (internal/database/replyindex), then builds the index with CREATE INDEX
--     CONCURRENTLY, which golang-migrate cannot run: it sends a file as one
--     execution, which Postgres runs as one transaction, and CONCURRENTLY is
--     refused inside a transaction. Until the index is valid, reply search is
--     off and the list says so.
--
-- An expression index on to_tsvector('english', body) was the first design. At
-- 1M replies a word in a quarter of them took 39 s: the match bitmap went lossy
-- and Postgres parsed every reply on those pages again to recheck. A stored
-- column is compared as it is.
--
-- The column is computed from the first 50,000 characters of a reply, the limit
-- 1.3.0 puts on a new reply. 1.2.0 had no limit, and Postgres refuses a tsvector
-- over 1,048,575 bytes (a megabyte of distinct words reaches it), which would fail
-- the insert or update and, in the server's fill, stop it for good. Words past the
-- first 50,000 characters of a very long reply are not searched. The fill
-- (internal/database/replyindex) must use the same limit.
--
-- The trigger fires on insert and on an update of body only, so the background
-- fill (which sets body_tsv) does not fire it. A row written before this
-- migration has body_tsv NULL until the fill reaches it; a NULL never matches.
ALTER TABLE ticket_replies ADD COLUMN IF NOT EXISTS body_tsv tsvector;

CREATE OR REPLACE FUNCTION ticket_replies_body_tsv() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.body_tsv := to_tsvector('english', left(NEW.body, 50000));
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS ticket_replies_body_tsv_trg ON ticket_replies;
CREATE TRIGGER ticket_replies_body_tsv_trg
    BEFORE INSERT OR UPDATE OF body ON ticket_replies
    FOR EACH ROW EXECUTE FUNCTION ticket_replies_body_tsv();
