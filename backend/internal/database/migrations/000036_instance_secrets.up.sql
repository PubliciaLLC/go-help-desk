-- instance_secrets stores sensitive per-instance keys, such as the JWT signing secret.
-- The name column is a simple allowlist: a future secret adds a name in its own migration.
-- created_at is recorded for audit purposes.
CREATE TABLE instance_secrets (
    name       TEXT PRIMARY KEY CHECK (name IN ('jwt_signing')),
    value      BYTEA NOT NULL CHECK (octet_length(value) >= 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
