-- Drops the descriptions. Their CHECK constraints go with the columns. The text
-- is lost, which is the point of a down: re-applying the up starts from none.
ALTER TABLE categories DROP COLUMN IF EXISTS description;
ALTER TABLE types DROP COLUMN IF EXISTS description;
ALTER TABLE items DROP COLUMN IF EXISTS description;
