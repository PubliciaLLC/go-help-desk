-- WebAuthn credentials: passkeys and hardware security keys.
--
-- A table rather than columns on users, because one person registers several
-- on purpose — a laptop, a phone, a spare key in a drawer — and that is the
-- feature rather than an edge case. The existing mfa_secret / mfa_enabled
-- columns are untouched: TOTP does not change and does not go away, and an
-- instance that migrates into this notices nothing until somebody registers a
-- key.
--
-- See docs/DESIGN.md → Authentication → Passkeys (WebAuthn) for what this
-- claims (phishing-resistance) and what it deliberately does not claim
-- (physical possession — a passkey is very often a synced credential).
CREATE TABLE webauthn_credentials (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- The credential id the authenticator generated, raw bytes.
    --
    -- UNIQUE in the schema rather than checked in Go. The specification says
    -- these are globally unique, and "the specification says so" is exactly
    -- the kind of claim this codebase puts a constraint behind. A
    -- read-then-insert also has a window between the read and the insert
    -- whatever it reads, which is the same reason the last-admin guard is one
    -- statement and not two.
    --
    -- BYTEA and not TEXT: it is not text, and base64 has more than one
    -- spelling. Storing the bytes means the comparison cannot be defeated by
    -- padding or by the URL-safe alphabet.
    credential_id BYTEA NOT NULL UNIQUE,

    -- COSE-encoded public key. Verification material, never a secret: the
    -- point of this feature is that losing this table to an attacker does not
    -- let them authenticate as anybody, which is not true of mfa_secret.
    public_key  BYTEA NOT NULL,

    -- The authenticator's signature counter as of the last assertion.
    --
    -- Stored and NOT enforced. The specification has it for clone detection,
    -- but most modern authenticators return zero every time, and a naive
    -- "must increase" rule locks those people out for nothing. One case is
    -- worth noticing and is logged rather than refused: a counter that was
    -- previously non-zero going backwards is a real clone signal with no
    -- false-positive cost. Detect, record, do not branch — the same shape as
    -- hashlookup's KnownMalicious in internal/reputation/circl.go.
    sign_count  BIGINT NOT NULL DEFAULT 0,

    -- How the authenticator can be reached: usb, nfc, ble, internal, hybrid.
    --
    -- Not kept for a future screen. It goes back out on the sign-in challenge
    -- as allowCredentials[].transports, which lets the browser skip
    -- authenticators that cannot satisfy the request instead of prompting for
    -- every method the account has ever registered. It has a job from the
    -- first release.
    transports  TEXT[] NOT NULL DEFAULT '{}',

    -- Which authenticator model this is. Nothing reads it yet; it is free at
    -- registration and unrecoverable afterwards, the same reasoning that keeps
    -- the unused CIRCL response fields in internal/reputation/circl.go.
    aaguid      BYTEA,

    -- The WebAuthn authenticator-data flags that say whether this credential
    -- is synced across devices (iCloud Keychain, Google Password Manager) or
    -- bound to one authenticator.
    --
    -- These are the only way an administrator auditing this instance can tell
    -- a hardware key from a synced passkey — which is the exact distinction
    -- the phishing-resistance claim turns on, since a synced credential keeps
    -- phishing-resistance and weakens possession. Not captured here at
    -- registration, the question is unanswerable forever after.
    backup_eligible BOOLEAN NOT NULL DEFAULT FALSE,
    backup_state    BOOLEAN NOT NULL DEFAULT FALSE,

    -- What its owner called it. Optional: an unnamed credential is shown by
    -- what can be derived from its transports and its age ("Security key,
    -- added 3 March"), which is more use than a bare date and leaks nothing
    -- the AAGUID would.
    name        TEXT NOT NULL DEFAULT '',

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Answers "which of these keys is still in use", which is what somebody
    -- wants to know before removing one and cannot be reconstructed
    -- afterwards. Written off the authentication path — a sign-in must not
    -- wait on it or fail because of it — following the shape already used for
    -- webhook dispatch in internal/server/notify/webhook.go. NULL until the
    -- credential has been used to sign in.
    last_used_at TIMESTAMPTZ
);

-- Every sign-in with a passkey, and every render of the "your keys" list,
-- reads this by user. The unique index on credential_id serves the other
-- direction, where an assertion arrives carrying only the credential id.
CREATE INDEX webauthn_credentials_user_id_idx ON webauthn_credentials (user_id);
