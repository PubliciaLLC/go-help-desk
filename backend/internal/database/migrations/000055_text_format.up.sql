-- Each description and each reply records how its text is to be shown:
-- 'plain' (as typed) or 'markdown' (#443).
--
-- DEFAULT 'plain' exists only to mark the rows that are already here: every
-- record written before this migration is plain text, because 1.2 promised
-- Markdown but never rendered it. It is dropped straight away. From then on
-- every INSERT must name the format, so a write path that forgets fails the
-- NOT NULL check in the test suite instead of silently picking a value. A
-- 'markdown' default would render text nobody wrote as Markdown; a 'plain'
-- default would hide the same mistake. No default is the only loud option.
--
-- No table rewrite: adding a column with a constant default is a catalogue
-- change only (PostgreSQL 11+; the supported minimum is 17).
ALTER TABLE tickets ADD COLUMN description_format text NOT NULL DEFAULT 'plain'
    CHECK (description_format IN ('plain', 'markdown'));
ALTER TABLE tickets ALTER COLUMN description_format DROP DEFAULT;

ALTER TABLE ticket_replies ADD COLUMN body_format text NOT NULL DEFAULT 'plain'
    CHECK (body_format IN ('plain', 'markdown'));
ALTER TABLE ticket_replies ALTER COLUMN body_format DROP DEFAULT;
