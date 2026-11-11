-- Which attachments an anonymous visitor is offered (#627).
--
-- Guests were shown no custom fields. They can now be offered some, but only
-- the ones an administrator chooses: reusing visible_on_new would have
-- published every field that staff see on the new-ticket form, names and
-- options included, to anyone on the internet the moment this migration ran.
-- So this is its own opt-in, per attachment, and off for every row that exists.
--
-- A guest picks a category and nothing below it, so the flag means something
-- only on a category's attachment; the service refuses it elsewhere.
--
-- A constant default, so Postgres adds the column without rewriting the table.
ALTER TABLE custom_field_assignments ADD COLUMN visible_to_guests BOOLEAN NOT NULL DEFAULT FALSE;

-- Long custom-field values (#626, #627).
--
-- A value is now limited to 255 characters, or 10,000 for a long-text field
-- (customfield.MaxChars), counted in characters, so a value can weigh 40 KB.
-- Migration 000009 indexed (field_def_id, value), and Postgres refuses an index
-- row over about 2,704 bytes: a value over roughly 2.7 KB of ordinary text could
-- not be saved at all, and took every other value of its request down with it.
--
-- The index serves lookups of one field's rows, and of a select field's rows by
-- value (the delete-an-option check, FirstHeldCustomFieldValue). Rows by value
-- never exceed 255 characters, the select limit, so the first 255 characters of
-- the value are all the index needs: at most 1,020 bytes a row. The query that
-- looks up by value now states the prefix, so the index still answers it.
DROP INDEX idx_cfv_field_def;
CREATE INDEX idx_cfv_field_def ON ticket_custom_field_values (field_def_id, left(value, 255));
