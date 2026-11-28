-- How many times a ticket has been reopened (#464). One column serves two jobs:
-- the "Reopened" badge reads reopen_count > 0 while the ticket is open, and the
-- priority bump on a reopen applies only when the count was 0 (the first
-- reopen). The count is never reset, so a ticket that is resolved and reopened
-- again is flagged again but does not climb a second level.
--
-- No backfill. Reopens before this migration were recorded only as audit
-- entries, which retention can prune, so a count rebuilt from them would be a
-- guess. A ticket reopened before the upgrade starts at 0 and its next reopen
-- counts as its first.
ALTER TABLE tickets ADD COLUMN reopen_count INTEGER NOT NULL DEFAULT 0 CHECK (reopen_count >= 0);

-- When the ticket was last reopened, and its Pending total (sla_paused_seconds
-- plus any open interval) at that instant. Every reopen is a point the SLA clock
-- restarts from, even when it lands on a priority with no SLA policy and so
-- starts no record: a later priority change that gives the ticket a record
-- back resumes from here, not from before the reopen. NULL: never reopened.
ALTER TABLE tickets
    ADD COLUMN last_reopened_at TIMESTAMPTZ,
    ADD COLUMN paused_seconds_at_last_reopen BIGINT NOT NULL DEFAULT 0 CHECK (paused_seconds_at_last_reopen >= 0);
