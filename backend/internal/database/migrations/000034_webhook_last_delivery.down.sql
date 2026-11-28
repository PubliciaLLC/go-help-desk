ALTER TABLE webhook_configs
    DROP COLUMN last_delivery_error,
    DROP COLUMN last_delivery_status,
    DROP COLUMN last_delivery_at;
