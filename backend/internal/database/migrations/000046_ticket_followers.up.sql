-- Ticket followers (#441, part D of #464): staff and administrators who asked
-- to hear about a ticket's activity without holding it.
--
-- ON DELETE CASCADE on both sides is for hard deletes only. Users are
-- soft-deleted, so a departed follower's row stays; the mail path checks that
-- the person is still active and can still see the ticket each time it sends.
-- The row is kept, not pruned, so following works again if their access
-- returns.
CREATE TABLE ticket_followers (
    ticket_id  UUID        NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (ticket_id, user_id)
);

-- The primary key serves "who follows this ticket". This serves the cascade
-- from a user and "which tickets does this person follow".
CREATE INDEX ticket_followers_user_idx ON ticket_followers (user_id);
