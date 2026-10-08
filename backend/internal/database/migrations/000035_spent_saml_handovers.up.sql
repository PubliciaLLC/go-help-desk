-- #337: the SAML library's "token" cookie is a stateless signed JWT that
-- /auth/saml/complete accepts in place of a fresh assertion. #340 tells the
-- browser to delete it on use, but a copy captured before then (proxy logs, a
-- device backup) stayed valid until it expired, and could mint a new app
-- session after every revocation.
--
-- This table makes it single-use: /complete records the hash of every cookie
-- it accepts, and refuses one it has already recorded. The primary key is
-- what decides, so two concurrent replays cannot both pass.
--
-- The hash, not the cookie: a row needs only to recognise a cookie, not to
-- reproduce it.
CREATE TABLE spent_saml_handovers (
    token_hash BYTEA       PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    spent_at   TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- Retention purge (see SpendSAMLHandover).
CREATE INDEX idx_spent_saml_handovers_spent_at ON spent_saml_handovers(spent_at);
