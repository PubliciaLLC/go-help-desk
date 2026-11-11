-- name: TakeRateLimit :one
-- Counts one attempt against a key and says where it landed. One statement,
-- so two attempts at once are serialised by the row lock and each gets its own
-- number: both cannot be told they were the last one under the limit.
--
-- The key's window is opened by this attempt when there is no row or the old
-- window has ended (window_end <= now), and keeps its original end otherwise,
-- so a window is never stretched by being used. Every SET expression reads the
-- row as it was before the update, which is what makes the three CASEs agree.
--
-- now comes from the caller, not the database, so a test can move a limiter's
-- clock; replicas whose clocks differ by a second differ by a second in when a
-- window ends, which no limit here is sensitive to.
INSERT INTO rate_limit_counters AS c (key, window_start, window_end, count)
VALUES (
    sqlc.arg(key)::text,
    sqlc.arg(now)::timestamptz,
    sqlc.arg(now)::timestamptz + make_interval(secs => sqlc.arg(window_seconds)::float8),
    1
)
ON CONFLICT (key) DO UPDATE
SET window_start = CASE WHEN c.window_end <= EXCLUDED.window_start THEN EXCLUDED.window_start ELSE c.window_start END,
    window_end   = CASE WHEN c.window_end <= EXCLUDED.window_start THEN EXCLUDED.window_end   ELSE c.window_end   END,
    count        = CASE WHEN c.window_end <= EXCLUDED.window_start THEN 1                     ELSE c.count + 1    END
RETURNING c.count, c.window_start, c.window_end;

-- name: RefundRateLimit :exec
-- Gives back one attempt TakeRateLimit counted, for a caller that spent before
-- the work and found the work did not happen. Only inside the window the attempt
-- was counted in: if the window has since ended and another begun, giving one
-- back would take a slot the new window never lost. Never below zero.
UPDATE rate_limit_counters
SET count = count - 1
WHERE key = sqlc.arg(key)::text
  AND window_start = sqlc.arg(window_start)::timestamptz
  AND count > 0;

-- name: GetRateLimitCount :one
-- The count in a key's live window, without counting an attempt. No row back
-- (never used, or its window has ended) means nothing has been spent.
SELECT count FROM rate_limit_counters
WHERE key = sqlc.arg(key)::text
  AND window_end > sqlc.arg(now)::timestamptz;

-- name: ResetRateLimit :exec
-- Clears a key's budget, after a successful sign-in.
DELETE FROM rate_limit_counters WHERE key = sqlc.arg(key)::text;

-- name: DeleteExpiredRateLimits :execrows
-- Removes up to batch rows whose window has ended. A bounded batch, called in a
-- loop, so that clearing a backlog (a burst of attempts on made-up email
-- addresses) is many short statements and not one that holds locks and writes a
-- huge amount of WAL at once. The window_end index serves it.
DELETE FROM rate_limit_counters
WHERE key IN (
    SELECT key FROM rate_limit_counters
    WHERE window_end <= sqlc.arg(now)::timestamptz
    ORDER BY window_end
    LIMIT sqlc.arg(batch)::int
);
