-- The admin's edited versions of the compiled email templates (#158).
--
-- No row means the template as shipped; reverting is deleting the row, and the
-- audit log is the history, so there is no versions table. The name is checked
-- against the code's registry rather than here: a template added or retired in
-- a later release should not need a migration to be editable.
--
-- Two reserved names, _header and _footer, hold the text that wraps every mail.
-- They live in this table so the HTML variants planned for a later release can
-- be added beside them as columns, not as a second table. For them subject is
-- the empty string.
--
-- updated_by is SET NULL, not RESTRICT: the text a person wrote should outlive
-- the account that wrote it.
CREATE TABLE email_template_overrides (
    name       TEXT PRIMARY KEY CHECK (name <> ''),
    subject    TEXT NOT NULL,
    body       TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID REFERENCES users (id) ON DELETE SET NULL
);
