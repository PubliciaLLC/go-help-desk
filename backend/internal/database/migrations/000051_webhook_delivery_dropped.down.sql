-- A dropped entry has no older class to go back to; 'other' is the nearest.
ALTER TABLE webhook_deliveries DROP CONSTRAINT webhook_deliveries_drop_reason_matches_result;
UPDATE webhook_deliveries SET result = 'other' WHERE result = 'dropped';
ALTER TABLE webhook_deliveries DROP COLUMN drop_reason;
ALTER TABLE webhook_deliveries DROP CONSTRAINT webhook_deliveries_result_check;
ALTER TABLE webhook_deliveries ADD CONSTRAINT webhook_deliveries_result_check
    CHECK (result IN ('ok', 'http_status', 'timeout', 'dns', 'tls',
                      'blocked_address', 'connection', 'other'));
