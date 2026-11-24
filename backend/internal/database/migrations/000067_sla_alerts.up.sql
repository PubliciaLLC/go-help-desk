-- SLA breach and at-risk notifications (#670).
--
-- Numbering: 000066 is reserved by the #616 branch. If #616 lands under a
-- different number, or this lands after it, renumber this file and its down
-- step at merge; nothing else in this migration depends on the number.
--
-- At-risk stamps (the sweep's "tell once" fact). The sweep writes these the
-- first time it sees a target at or past 80% while outstanding and below 100%,
-- and never clears them, so a crossing is announced at most once. NULL: not yet
-- seen at risk. Like the breach stamps, only the sweep writes them.
ALTER TABLE sla_records
    ADD COLUMN response_at_risk_at   TIMESTAMPTZ,
    ADD COLUMN resolution_at_risk_at TIMESTAMPTZ;

-- Per-person switch for SLA mail (the fourth switch, next to the three of
-- 000042). On by default for every existing row, as those are, and an
-- administrator can force it with staff_email_sla_override (set below).
ALTER TABLE users
    ADD COLUMN email_notify_sla BOOLEAN NOT NULL DEFAULT TRUE;

-- The off switch for both SLA events (webhooks and email alike).
--
-- "Upgraded" means a person already exists, as in 000052: a fresh install
-- migrates with an empty users table and gets true, so an administrator who
-- turns SLA tracking on there expects a breach to be announced. An upgraded
-- install gets false: on upgrade, existing "*" webhooks would start receiving
-- two event names they have never seen, and an instance already running SLA
-- would start announcing on its first boot. Service accounts (000041) are not
-- people, so they do not count.
--
-- ON CONFLICT DO NOTHING: a value already stored is an administrator's choice
-- and wins.
INSERT INTO settings (key, value)
SELECT 'sla_alerts_enabled', to_jsonb(NOT EXISTS (SELECT 1 FROM users WHERE NOT service_account))
ON CONFLICT (key) DO NOTHING;

-- Staff SLA mail starts off on an upgraded install, as the three staff email
-- overrides do (000052), and for the same reason: an upgraded install with
-- SMTP configured must not begin mailing staff on first boot. Same condition
-- and same ON CONFLICT DO NOTHING as 000052.
INSERT INTO settings (key, value)
SELECT 'staff_email_sla_override', '"off"'::jsonb
WHERE EXISTS (SELECT 1 FROM users WHERE NOT service_account)
ON CONFLICT (key) DO NOTHING;
