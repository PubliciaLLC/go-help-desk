-- The index 1.2.0 and 1.3.0-beta had cannot hold a long value (index rows over
-- about 2,704 bytes are refused), so a value the 10,000-character limit allowed
-- is cut to what the old index takes before the old index is built: 650
-- characters is at most 2,600 bytes. Values that already fit are not touched.
-- The cut text is lost; see "Rolling Back to 1.2.0" in the upgrade notes.
UPDATE ticket_custom_field_values SET value = left(value, 650) WHERE octet_length(value) > 2600;
DROP INDEX idx_cfv_field_def;
CREATE INDEX idx_cfv_field_def ON ticket_custom_field_values (field_def_id, value);
ALTER TABLE custom_field_assignments DROP COLUMN visible_to_guests;
