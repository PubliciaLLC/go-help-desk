-- Delivery history for webhooks, and what Redeliver replays (#448).
--
-- One row per delivery (one event to one hook), not per attempt: id is the
-- X-GHD-Delivery a receiver saw, and every retry updates the same row. The
-- application keeps the newest 25 per hook; the table has no limit of its own.
--
-- Like webhook_configs.last_delivery_error (000034), result is a CLASS, never a
-- message: a URL can carry a token and a response body can carry anything. The
-- CHECK makes storing text impossible rather than merely discouraged. Keep the
-- list in step with notify.DeliveryErrors; 'ok' is the success value.
--
-- event is the queued event (ids, subject, tracking number, a public reply's
-- text), kept only so Redeliver can send it again. It is never returned by the
-- API, and it holds no recipient address and no guest credential. It is deleted
-- with the row and with the hook.
--
-- This number was the next free one when it was written (000036 belongs to
-- #465); renumber if another branch has taken it by merge time.
CREATE TABLE webhook_deliveries (
    id              uuid        PRIMARY KEY,
    webhook_id      uuid        NOT NULL REFERENCES webhook_configs (id) ON DELETE CASCADE,
    event_type      text        NOT NULL,
    event           jsonb       NOT NULL,
    created_at      timestamptz NOT NULL,
    last_attempt_at timestamptz NOT NULL,
    status          integer     NOT NULL DEFAULT 0
        CHECK (status = 0 OR status BETWEEN 100 AND 999),
    result          text        NOT NULL
        CHECK (result IN ('ok', 'http_status', 'timeout', 'dns', 'tls',
                          'blocked_address', 'connection', 'other')),
    attempts        integer     NOT NULL DEFAULT 1,
    retrying        boolean     NOT NULL DEFAULT false
);

-- The history list and the cap both read the newest rows of one hook.
CREATE INDEX webhook_deliveries_hook_idx ON webhook_deliveries (webhook_id, created_at DESC, id DESC);
