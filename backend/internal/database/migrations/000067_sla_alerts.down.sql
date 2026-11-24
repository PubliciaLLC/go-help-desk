-- Drops the three columns. The settings rows are deliberately left alone, as
-- 000052's down step leaves its rows: the up step cannot be told apart
-- afterwards from an administrator's own choice, so deleting sla_alerts_enabled
-- or staff_email_sla_override would lose a choice the administrator stored.
-- Older code never reads these rows.
ALTER TABLE sla_records
    DROP COLUMN response_at_risk_at,
    DROP COLUMN resolution_at_risk_at;

ALTER TABLE users
    DROP COLUMN email_notify_sla;
