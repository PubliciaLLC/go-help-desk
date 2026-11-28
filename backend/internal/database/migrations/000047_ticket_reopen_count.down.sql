ALTER TABLE tickets
    DROP COLUMN paused_seconds_at_last_reopen,
    DROP COLUMN last_reopened_at,
    DROP COLUMN reopen_count;
