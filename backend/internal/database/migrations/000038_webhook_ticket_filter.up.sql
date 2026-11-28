-- A per-hook ticket filter (#413). '{}' is "no filter": every existing hook
-- keeps firing for every ticket, with no data migration. The shape inside is
-- notification.WebhookFilter, written only through authstore. The CHECK
-- refuses anything that is not an object, so a stray scalar never reaches the
-- decoder. Named ticket_filter, not filter: FILTER is an SQL keyword.
ALTER TABLE webhook_configs
    ADD COLUMN ticket_filter jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(ticket_filter) = 'object');
