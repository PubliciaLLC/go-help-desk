CREATE TABLE ticket_status_history (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id           UUID        NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    from_status_id      UUID        REFERENCES statuses(id),
    to_status_id        UUID        NOT NULL REFERENCES statuses(id),
    changed_by_user_id  UUID        REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ticket_status_history_ticket_id
    ON ticket_status_history(ticket_id, created_at);
