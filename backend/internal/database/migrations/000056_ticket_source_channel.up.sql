-- Each ticket records how it arrived (#130): web, api, mcp, guest or phone.
-- The server derives it from the entry point; the only value a person chooses
-- is 'phone', when staff file on someone's behalf in the web form. 'email' is
-- reserved for inbound mail and is left out of the CHECK on purpose: adding it
-- later is a migration, which is the price of a closed list.
--
-- DEFAULT 'web' exists only to mark the rows that are already here. Erik chose
-- 'web' over an "unknown" value knowing it invents history: some of these
-- tickets came in by API or MCP, and nothing recorded which. The 1.3.0 upgrade
-- notes say so. The default is dropped straight away, as for description_format
-- in 000055: from then on every INSERT must name the channel, so a new entry
-- point that forgets fails the NOT NULL check in the test suite instead of
-- being recorded as 'web'. The value is derived on the server, so a default
-- would be less risky than for caller-supplied data, but the column's whole
-- worth is being right and it cannot be corrected afterwards.
--
-- No table rewrite: adding a column with a constant default is a catalogue
-- change only (PostgreSQL 11+; the supported minimum is 17).
ALTER TABLE tickets ADD COLUMN source_channel text NOT NULL DEFAULT 'web'
    CHECK (source_channel IN ('web', 'api', 'mcp', 'guest', 'phone'));
ALTER TABLE tickets ALTER COLUMN source_channel DROP DEFAULT;
