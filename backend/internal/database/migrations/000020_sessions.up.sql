-- Server-side sessions.
--
-- Sessions were stateless signed cookies, so nothing could invalidate one: a
-- disabled account kept working for the cookie's 30-day life, logout only
-- cleared the browser's copy, and an admin clearing MFA left a stale
-- MFAPassed=true in every existing cookie. Holding the state here makes
-- revocation a DELETE that takes effect on the next request.
--
-- It is also the only shape that can ever honour federated logout: a SAML
-- LogoutRequest carries a SessionIndex and an OIDC back-channel logout token
-- carries a sid, and both identify a SESSION rather than a user. Neither is
-- captured today, so no column for them yet — but with a table to attach them
-- to, that becomes an additive migration instead of a redesign.
CREATE TABLE sessions (
    -- Opaque random id; the cookie carries this and nothing else.
    id         TEXT PRIMARY KEY,

    -- NULL until the session belongs to someone. The OIDC flow writes state
    -- (nonce, PKCE verifier) into a session before anyone has authenticated.
    user_id    UUID REFERENCES users(id) ON DELETE CASCADE,

    -- gob-encoded session values — the payload the cookie used to carry.
    data       BYTEA       NOT NULL,

    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Revoking every session a user holds is the common operation: disable, role
-- change, password change, MFA reset.
CREATE INDEX idx_sessions_user_id ON sessions(user_id) WHERE user_id IS NOT NULL;

-- The expiry sweep.
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
