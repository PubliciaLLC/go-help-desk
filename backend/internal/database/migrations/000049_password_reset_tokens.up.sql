-- Password reset by email (#451): the link a "forgot password" request is
-- answered with.
--
-- Only the SHA-256 of the token is stored, as for guest links and API keys, so
-- a copy of the database holds nothing that can be turned into a reset. The raw
-- token exists in the email and nowhere else; it is made when the mail is sent
-- (the outbox row that asks for the mail holds the address that was typed, not
-- a token).
--
-- One row per account, so a new request replaces the earlier link: at most one
-- link works at a time, and a stack of old ones in an inbox is not a stack of
-- live ones. The row is deleted when the link is used (single use), and is
-- ignored once expires_at passes. An expired row is not swept: there is at most
-- one per account, and the next request for that account replaces it.
--
-- ON DELETE CASCADE is for hard deletes only; users are soft-deleted, and the
-- redeem statement refuses a deleted or disabled account itself.
CREATE TABLE password_reset_tokens (
    user_id    UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT        NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
