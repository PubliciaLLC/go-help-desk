-- #249: these two columns are also the ONLY place migration
-- 000028_repair_resolved_status_invariant.up.sql's fact/estimate distinction
-- lives (a permanently-NULL resolution_elapsed_at_met_seconds marks a
-- resolution instant recovered only from the updated_at fallback, never a
-- confirmed fact — see that file's LIMITS block). Dropping them here does not
-- just remove data 000027 can regenerate; it discards information 000028 has
-- no other way to recover. Rewinding through this down migration and back up
-- (unlike a 028-only down/up) loses that distinction for good: 000027's own
-- backfill below unconditionally re-freezes elapsed from whatever
-- sla_records.resolved_at/first_response_at already hold, indistinguishable
-- afterward from a fact, and the next 000028 up will stamp a breach from an
-- estimated instant it previously and correctly left unstamped. Accepted and
-- pinned by TestMigration_RewindThrough000027LosesEstimatedMarkerAndReStamps,
-- not a bug — see 000028's LIMITS block for the full explanation.
ALTER TABLE sla_records
    DROP COLUMN response_elapsed_at_met_seconds,
    DROP COLUMN resolution_elapsed_at_met_seconds;
