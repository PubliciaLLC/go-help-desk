-- No data is lost: body_tsv and its indexes are derived from body. The indexes
-- are built by the server after the migration (internal/database/replyindex),
-- so they are dropped here too. Dropping the column drops its indexes with it;
-- the explicit drops are for an index left behind under another definition.
DROP INDEX IF EXISTS ticket_replies_body_tsv_idx;
DROP INDEX IF EXISTS ticket_replies_created_at_idx;
DROP TRIGGER IF EXISTS ticket_replies_body_tsv_trg ON ticket_replies;
DROP FUNCTION IF EXISTS ticket_replies_body_tsv();
ALTER TABLE ticket_replies DROP COLUMN IF EXISTS body_tsv;
