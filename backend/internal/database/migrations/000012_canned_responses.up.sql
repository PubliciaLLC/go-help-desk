-- Canned responses — reusable reply templates inserted into ticket replies.
-- Scope: both NULL = global; category_id set = whole category;
-- category_id + type_id = that category and type only.

CREATE TABLE canned_responses (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    body        TEXT        NOT NULL,
    category_id UUID        REFERENCES categories (id) ON DELETE CASCADE,
    type_id     UUID        REFERENCES types (id)      ON DELETE CASCADE,
    sort_order  INTEGER     NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- A type-scoped response must also name its category.
    CONSTRAINT canned_responses_scope_chk CHECK (type_id IS NULL OR category_id IS NOT NULL)
);

CREATE INDEX canned_responses_scope_idx ON canned_responses (category_id, type_id);
