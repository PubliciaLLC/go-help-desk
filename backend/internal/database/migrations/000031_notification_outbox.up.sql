-- Notifications are delivered from here, not on the request (#164).
--
-- Delivering on the request made every caller wait on the mail server, and
-- made the guest-resend endpoint, which answers identically for a match and a
-- miss, leak the answer through its timing: a match dialled SMTP, a miss did
-- not. A request now writes a row and returns; a worker sends.
--
-- One row per event per channel, so a channel that fails is retried on its
-- own and one that succeeded is not sent twice.
--
-- event holds no secret. A guest's access token is created by the worker at
-- send time (guest_link); the raw token is never stored here, because the
-- guest token table holds only hashes and a copy here would undo that.
CREATE TABLE notification_outbox (
    id            uuid        PRIMARY KEY,
    channel       text        NOT NULL,
    event         jsonb       NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    available_at  timestamptz NOT NULL DEFAULT now(),
    claimed_until timestamptz,
    attempts      integer     NOT NULL DEFAULT 0,
    last_error    text,
    failed_at     timestamptz
);

-- The worker's claim: rows due now, not given up on.
CREATE INDEX notification_outbox_due_idx ON notification_outbox (available_at)
    WHERE failed_at IS NULL;
-- Cleanup of rows that were given up on.
CREATE INDEX notification_outbox_failed_idx ON notification_outbox (failed_at)
    WHERE failed_at IS NOT NULL;
