-- The API has always treated an omitted visible_on_new as false (the handler
-- decoded a plain bool), while this column defaulted to TRUE. Anything that
-- inserted without going through the handler got the other answer. The column
-- now says what the API says. Existing rows are untouched: they hold whatever
-- was stored.
ALTER TABLE custom_field_assignments ALTER COLUMN visible_on_new SET DEFAULT FALSE;
