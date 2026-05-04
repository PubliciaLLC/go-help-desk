CREATE TABLE pending_registrations (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT        NOT NULL,
    display_name  TEXT        NOT NULL,
    password_hash TEXT        NOT NULL,
    token         UUID        NOT NULL DEFAULT gen_random_uuid(),
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX pending_registrations_email_idx ON pending_registrations (lower(email));
CREATE UNIQUE INDEX pending_registrations_token_idx ON pending_registrations (token);
