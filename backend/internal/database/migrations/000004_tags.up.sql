-- Tags — case-insensitive, stored lowercase, soft-deletable by admins.

CREATE TABLE tags (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL UNIQUE,   -- always lowercase
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX tags_name_idx ON tags (name);
CREATE INDEX tags_active_idx ON tags (name) WHERE deleted_at IS NULL;

CREATE TABLE ticket_tags (
    ticket_id UUID NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    tag_id    UUID NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (ticket_id, tag_id)
);
