-- Full-text search over ticket subject + description, replacing substring
-- ILIKE with tokenized, weighted, ranked search. Subject is weighted higher
-- ('A') than description ('B') so a match in the subject line ranks above
-- one buried in a long description. Generated + stored so it's always in
-- sync with the source columns without application-level upkeep.

ALTER TABLE tickets ADD COLUMN search_vector tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(subject, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B')
    ) STORED;

CREATE INDEX tickets_search_vector_idx ON tickets USING GIN (search_vector);
