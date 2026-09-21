-- Per-ticket access tokens for guests.
--
-- A guest has no account, so the link in their email is the whole credential.
-- Holding it here rather than in a column on tickets buys three things a
-- column cannot: re-issue without invalidating the link already in someone's
-- inbox, revocation on close as a DELETE, and a record of whether a link was
-- ever opened.
--
-- Only the hash is stored, exactly as for API keys. The raw token exists in
-- one email and nowhere else; it cannot be recovered from a database dump, a
-- backup, or by anyone with read access to this table.
CREATE TABLE guest_access_tokens (
    id           UUID PRIMARY KEY,

    -- One token names one ticket. There is no list endpoint and no id
    -- parameter to change, so this column is the whole of a guest's authority.
    ticket_id    UUID        NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,

    -- SHA-256 of the raw token, hex encoded. UNIQUE so a lookup is an index
    -- hit rather than a scan, and so a hash collision cannot silently create
    -- two tokens that both work.
    token_hash   TEXT        NOT NULL UNIQUE,

    -- Outer bound only. On an active ticket the token is replaced every time
    -- something happens that the guest is told about, so it rarely lives this
    -- long.
    expires_at   TIMESTAMPTZ NOT NULL,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- NULL until the link is first opened. Lets an operator tell "the customer
    -- never saw it" from "the customer read it and did not reply", which are
    -- different problems.
    last_used_at TIMESTAMPTZ
);

-- Rotation and revocation both work by ticket: delete what the ticket has,
-- write the replacement. Not UNIQUE on ticket_id — a rotation inserts before
-- the old row is gone within a transaction, and a future "two devices" case
-- should not need a migration.
CREATE INDEX idx_guest_access_tokens_ticket_id ON guest_access_tokens(ticket_id);

-- The expiry sweep, mirroring sessions.
CREATE INDEX idx_guest_access_tokens_expires_at ON guest_access_tokens(expires_at);

-- ticket_replies.guest_token has existed since 000001 and has held NULL in
-- every row ever written: nothing has ever assigned it. It is also on the
-- wrong table — a per-reply guest identity rather than per-ticket access — so
-- leaving it in place would invite someone to wire up the wrong thing.
ALTER TABLE ticket_replies DROP COLUMN guest_token;
