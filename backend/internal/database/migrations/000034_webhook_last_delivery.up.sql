-- The latest delivery result per webhook (#157). Before this a failed
-- delivery was visible only in the server log: the webhook channel's outbox
-- row is shared by every hook and settled as soon as delivery has started, so
-- it cannot carry a per-hook result.
--
-- last_delivery_at NULL means "never delivered to" (or the URL changed since).
-- last_delivery_status is the HTTP status, 0 when no response came back.
-- last_delivery_error is a CLASS, '' on success. It is deliberately not free
-- text: a webhook URL can carry a secret token, and error strings and response
-- bodies can carry anything. The CHECK makes storing a message impossible
-- rather than merely discouraged. Keep the list in step with
-- notify.DeliveryErrors.
ALTER TABLE webhook_configs
    ADD COLUMN last_delivery_at     timestamptz,
    ADD COLUMN last_delivery_status integer NOT NULL DEFAULT 0
        CHECK (last_delivery_status = 0 OR last_delivery_status BETWEEN 100 AND 999),
    ADD COLUMN last_delivery_error  text    NOT NULL DEFAULT ''
        CHECK (last_delivery_error IN ('', 'http_status', 'timeout', 'dns', 'tls',
                                       'blocked_address', 'connection', 'other'));
