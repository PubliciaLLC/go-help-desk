-- Durable counters for the rate limits that used to live in each server
-- process's memory (#621): reporter_email, guest uploads and refusals, guest
-- link resend, forgot-password mail, the sign-in and signup throttle, and the
-- email template test send.
--
-- In memory, a restart cleared every count and N replicas allowed N times the
-- limit. One row per key, in the database every replica shares.
--
-- Each key has a window of its own, opened by its first attempt and ending
-- window_end later. There is no table-wide reset, so a key is never refused
-- for longer than its own window, and never let off early by another key's.
--
-- Rows are not kept past their window. A key's row is replaced the next time it
-- is used, and the periodic sweep deletes the rest (DeleteExpiredRateLimits),
-- because some keys are chosen by whoever is calling (an email address typed
-- into the sign-in form), and a table that only grew would be a way to fill the
-- disk. window_end is indexed for that sweep.
--
-- Logged, not UNLOGGED: an unlogged table is emptied by a crash, which is the
-- restart-clears-the-limit defect this table exists to remove.
CREATE TABLE rate_limit_counters (
    key          text        PRIMARY KEY,
    window_start timestamptz NOT NULL,
    window_end   timestamptz NOT NULL,
    count        integer     NOT NULL CHECK (count >= 0)
);

CREATE INDEX rate_limit_counters_window_end_idx ON rate_limit_counters (window_end);
