-- name: GetSetting :one
SELECT value FROM settings WHERE key = $1;

-- name: SetSetting :exec
INSERT INTO settings (key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- name: SetSettingIfEmpty :execrows
-- Writes the value only when the setting is an empty string or not there yet.
-- The row lock on the conflicting row is the gate: a second writer waits for
-- the first to commit and then finds the value filled, so it affects no row.
INSERT INTO settings (key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
WHERE settings.value = '""'::jsonb;

-- name: ListSettings :many
SELECT key, value FROM settings ORDER BY key;
