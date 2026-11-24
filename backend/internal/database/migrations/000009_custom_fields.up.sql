-- Global custom field definitions
CREATE TABLE custom_field_defs (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL UNIQUE,
    field_type TEXT        NOT NULL CHECK (field_type IN ('text', 'textarea', 'number', 'select')),
    options    JSONB,
    sort_order INTEGER     NOT NULL DEFAULT 0,
    active     BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Assignments of field defs to CTI nodes (category, type, or item)
CREATE TABLE custom_field_assignments (
    id              UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    field_def_id    UUID    NOT NULL REFERENCES custom_field_defs(id) ON DELETE CASCADE,
    scope_type      TEXT    NOT NULL CHECK (scope_type IN ('category', 'type', 'item')),
    scope_id        UUID    NOT NULL,
    sort_order      INTEGER NOT NULL DEFAULT 0,
    visible_on_new  BOOLEAN NOT NULL DEFAULT TRUE,
    required_on_new BOOLEAN NOT NULL DEFAULT FALSE,
    UNIQUE (field_def_id, scope_type, scope_id)
);

-- Normalized ticket field values (TEXT for all types — filterable via WHERE clause)
CREATE TABLE ticket_custom_field_values (
    ticket_id    UUID        NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    field_def_id UUID        NOT NULL REFERENCES custom_field_defs(id) ON DELETE RESTRICT,
    value        TEXT        NOT NULL DEFAULT '',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (ticket_id, field_def_id)
);

CREATE INDEX idx_cfv_field_def ON ticket_custom_field_values(field_def_id, value);
