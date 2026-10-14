-- Starts the clock for #453: accounts still on a password hash below the current
-- bcrypt cost are refused at sign-in once password_rehash_grace_days have passed
-- since this moment, which is the first time 1.3.0 ran against this database.
-- Written once; ON CONFLICT DO NOTHING keeps a re-run from restarting it.
-- Not a known settings key, so PATCH /admin/settings cannot change it.
INSERT INTO settings (key, value)
VALUES ('password_rehash_started_at', to_jsonb(to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))
ON CONFLICT (key) DO NOTHING;
