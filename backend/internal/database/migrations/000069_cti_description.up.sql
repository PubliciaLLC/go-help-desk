-- Migration 000069: an optional description on each category, type and item
-- (1.3.0). Shown beneath the Category/Type/Item chooser on the ticket forms;
-- the most specific one of the chosen levels wins (docs/DESIGN.md, Data Model ->
-- Ticket Classification -> Descriptions).
--
-- 000069 was the next free number on v1.3.0-beta when this was written (000068 is
-- the newest there). Another open branch that also adds a migration takes the
-- next one and renumbers on merge: nothing here depends on the number.
--
-- NOT NULL DEFAULT '' rather than a nullable column: "none" has one spelling, so
-- no reader needs two branches. The default is kept (unlike 000056) because it is
-- not history to be marked: it is the value of every node that has no description,
-- and the INSERTs that exist today do not name the column.
--
-- The CHECK is the backstop for the 500-character limit the service enforces
-- (category.MaxDescription); char_length counts characters, as the service does.
-- No table rewrite: a constant default is a catalogue change only, and the three
-- tables are small.
ALTER TABLE categories ADD COLUMN description text NOT NULL DEFAULT ''
    CONSTRAINT categories_description_len CHECK (char_length(description) <= 500);
ALTER TABLE types ADD COLUMN description text NOT NULL DEFAULT ''
    CONSTRAINT types_description_len CHECK (char_length(description) <= 500);
ALTER TABLE items ADD COLUMN description text NOT NULL DEFAULT ''
    CONSTRAINT items_description_len CHECK (char_length(description) <= 500);
