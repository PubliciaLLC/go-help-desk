-- A queued webhook retry that is dropped (the hook was disabled, unsubscribed
-- from the event, pointed at a new URL, its ticket filter tightened, or the body
-- can no longer be built) used to leave its history entry saying "retrying"
-- for ever (#499). It now becomes result 'dropped', with the reason beside it.
--
-- Like result, drop_reason is a CLASS, never text, and the CHECK makes text
-- impossible. Keep the list in step with notify.DropReasons. A deleted hook is
-- not a reason: its history is deleted with it.
ALTER TABLE webhook_deliveries DROP CONSTRAINT webhook_deliveries_result_check;
ALTER TABLE webhook_deliveries ADD CONSTRAINT webhook_deliveries_result_check
    CHECK (result IN ('ok', 'http_status', 'timeout', 'dns', 'tls',
                      'blocked_address', 'connection', 'other', 'dropped'));

ALTER TABLE webhook_deliveries
    ADD COLUMN drop_reason text NOT NULL DEFAULT ''
        CHECK (drop_reason IN ('', 'disabled', 'unsubscribed', 'url_changed', 'filter', 'unbuildable')),
    ADD CONSTRAINT webhook_deliveries_drop_reason_matches_result
        CHECK ((result = 'dropped') = (drop_reason <> ''));
