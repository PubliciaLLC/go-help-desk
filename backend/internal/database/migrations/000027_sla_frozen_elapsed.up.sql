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

-- #226(b): a ticket closed WITHOUT ever being resolved (an admin moving it
-- straight from an open status to Closed, or the reopen window simply never
-- being used) has resolved_at NULL under the OLD rules — nothing wrote it
-- before #220 taught close()/UpdateStatus to always record a resolution on
-- the way into Closed. Left NULL, the indicator has no MetAt to freeze
-- against and keeps computing a live, ever-growing Elapsed(t, now) against a
-- ticket that will never move again. Backfill it from whichever of the
-- ticket's own resolved_at/closed_at is available — matching #220's "closed
-- without resolving still gets a resolution recorded" rule, and exactly what
-- RecordResolved would have written had this ticket reached Closed under the
-- new code. COALESCE(t.resolved_at, t.closed_at) rather than t.closed_at
-- alone: a ticket already correctly Resolved-then-Closed has t.resolved_at
-- set to the REAL, earlier resolution instant, and that must win over the
-- later closed_at (see #227 — the same "the real instant, not the closing
-- one" rule this migration's backfill has to respect too).
UPDATE sla_records r
SET resolved_at = COALESCE(t.resolved_at, t.closed_at)
FROM tickets t
WHERE t.id = r.ticket_id
  AND r.resolved_at IS NULL
  AND t.closed_at IS NOT NULL;

-- Freeze resolution_elapsed_at_met_seconds for exactly the rows the backfill
-- above just gave a resolved_at to (its own WHERE excludes every row already
-- carrying one, including the rows the first resolution_elapsed_at_met_seconds
-- pass above already froze) — same formula, now with a value to compute it
-- from.
UPDATE sla_records r
SET resolution_elapsed_at_met_seconds = GREATEST(
        0,
        EXTRACT(EPOCH FROM (r.resolved_at - t.created_at))::bigint - t.sla_paused_seconds
    )
FROM tickets t
WHERE t.id = r.ticket_id
  AND r.resolved_at IS NOT NULL
  AND r.resolution_elapsed_at_met_seconds IS NULL;

-- #226(a): a ticket resolved without ever getting a prior staff reply was
-- allowed under the OLD rules, before #219 taught RecordResolved that "a
-- resolution is a response in every practical sense." Left NULL,
-- IsResponseBreached computes a live Elapsed(t, now) against a target that
-- was, in fact, met the moment the ticket resolved — reading a promptly
-- resolved ticket as a permanent response breach the instant wall-clock time
-- passes the response target. Worse: RecordResolved now returns early once
-- resolved_at is already set (#220's no-op guard), so nothing in the running
-- code will ever backfill this after upgrade — the sweep would otherwise be
-- the backstop, but it still selects this row every tick (first_response_at
-- IS NULL and not yet stamped) and, on its very first run after upgrade,
-- would stamp a PERMANENT false response_breached_at against it, since nothing
-- ever sets first_response_at through the normal code path for an
-- already-Resolved/Closed ticket. Backfilling here, before that first sweep
-- ever runs, is what prevents that.
--
-- This runs AFTER the resolved_at backfill above (not before): a ticket that
-- was BOTH closed-without-resolving AND never separately responded to needs
-- its resolved_at filled in first, so this pass has a resolution instant to
-- treat as the response too — first_response_at = resolved_at, exactly #219's
-- rule, whichever door originally supplied that resolved_at.
UPDATE sla_records r
SET first_response_at = r.resolved_at
FROM tickets t
WHERE t.id = r.ticket_id
  AND r.resolved_at IS NOT NULL
  AND r.first_response_at IS NULL;

-- Freeze response_elapsed_at_met_seconds for exactly the rows the backfill
-- above just gave a first_response_at to, using the same pending-aware clip
-- as the first response_elapsed_at_met_seconds pass above (see #222) — first
-- response and resolution landed at the same instant for these rows, so the
-- same formula applies.
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
  AND r.first_response_at IS NOT NULL
  AND r.response_elapsed_at_met_seconds IS NULL;
