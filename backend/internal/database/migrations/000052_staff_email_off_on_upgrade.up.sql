-- Staff email starts OFF on an upgraded install (#441, 000042).
--
-- 000042 turned the per-user staff-email switches on for every row, so a 1.2
-- install that already has SMTP configured would begin mailing every staff
-- member about their tickets the moment 1.3.0 booted, before an administrator
-- could reach Settings -> Email. Storing the three administrator overrides as
-- "off" holds that mail back until the administrator turns it on.
--
-- "Upgraded" means a person already exists. A fresh install migrates with an
-- empty users table (the first administrator is created later, at /setup), so
-- it gets nothing here and the defaults apply. Service accounts (000041) are
-- not people, so they do not count.
--
-- ON CONFLICT DO NOTHING: a value already stored is an administrator's choice
-- and wins. On a 1.2 database the keys cannot exist yet; on a beta database
-- they may.
INSERT INTO settings (key, value)
SELECT k, '"off"'::jsonb
FROM unnest(ARRAY[
    'staff_email_assigned_override',
    'staff_email_activity_override',
    'staff_email_group_override'
]) AS k
WHERE EXISTS (SELECT 1 FROM users WHERE NOT service_account)
ON CONFLICT (key) DO NOTHING;
