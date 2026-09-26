-- Freeze elapsed-toward-target as a NUMBER at the moment a target is met, not
-- just the timestamp it was met at.
--
-- sla.Elapsed(t, at) computes "at - created_at - sla_paused_seconds", clipping
-- only the currently-open pause interval to at. sla_paused_seconds is a single
-- accumulated total with no per-interval timestamps, so a Pending interval
-- that CLOSES after at still gets subtracted from a reading taken at that
-- earlier at. Concretely: a late response is correctly red the moment it is
-- recorded, but if the ticket later goes Pending and comes back, the ticket's
-- sla_paused_seconds grows, and re-reading Elapsed(t, first_response_at) with
-- that grown total silently shrinks the reading and can flip an already-late
-- response back to green. A met target must never move again.
--
-- These columns hold the number sla.Service.RecordFirstResponse /
-- RecordResolved compute and store at the instant they run, from the ticket
-- row as it stood then — before any later pause activity exists to
-- contaminate it. sla.StatusFor reads this column for a met target instead of
-- recomputing Elapsed against the ticket's current, possibly-since-changed
-- pause state.
ALTER TABLE sla_records
    ADD COLUMN response_elapsed_at_met_seconds   BIGINT,
    ADD COLUMN resolution_elapsed_at_met_seconds BIGINT;

-- Best-effort backfill for any record already carrying a timestamp (from
-- before this migration, or from a beta instance): the same formula, computed
-- once, from each ticket's row as it stands now. This cannot be exact for a
-- ticket that was already paused again after the target was met — that is
-- precisely the bug this migration fixes, and there is no way to recover the
-- pause state as it stood at the earlier instant. It is still strictly better
-- than leaving the column NULL (which falls back to the same live recompute
-- these rows already display today), and it is a one-time correction, not a
-- pattern anything relies on going forward.
-- The same pending-aware clip sla.Elapsed applies at read time: a ticket
-- Pending at upgrade time (migration 000025, run just before this one in the
-- same upgrade, populates pending_since for every currently-Pending ticket)
-- whose first_response_at landed AFTER it went Pending would otherwise be
-- backfilled too large by exactly (first_response_at - pending_since) — the
-- open pause interval sla.Elapsed clips away but this one-time backfill
-- formula did not. See #222.
UPDATE sla_records r
SET response_elapsed_at_met_seconds = GREATEST(
        0,
        EXTRACT(EPOCH FROM (r.first_response_at - t.created_at))::bigint - t.sla_paused_seconds
            - CASE
                  WHEN t.pending_since IS NOT NULL
                  THEN GREATEST(0, EXTRACT(EPOCH FROM (r.first_response_at - t.pending_since)))::bigint
                  ELSE 0
              END
    )
FROM tickets t
WHERE t.id = r.ticket_id
  AND r.first_response_at IS NOT NULL;

UPDATE sla_records r
SET resolution_elapsed_at_met_seconds = GREATEST(
        0,
        EXTRACT(EPOCH FROM (r.resolved_at - t.created_at))::bigint - t.sla_paused_seconds
    )
FROM tickets t
WHERE t.id = r.ticket_id
  AND r.resolved_at IS NOT NULL;
