-- Repair legacy rows that predate the invariant AutoClose now assumes:
--
--   | current status   | resolved_at               | closed_at |
--   |-------------------|---------------------------|-----------|
--   | system Resolved   | set                       | NULL      |
--   | system Closed     | kept as is (set or NULL)  | set       |
--   | anything else     | NULL                      | NULL      |
--
-- (Round 4 adversarial review, #237-241, replaced the older, narrower
-- statement of this invariant that used to sit here — see git history for
-- the version that only promised "Resolved has resolved_at set and nothing
-- else does", which was wrong: a Closed ticket legitimately keeps
-- resolved_at from before it was closed.)
--
-- Before this, UpdateStatus (and any other second door into these statuses)
-- could move a ticket off Resolved without clearing resolved_at, move it off
-- Closed without clearing closed_at, or move it INTO Resolved/Closed without
-- ever stamping the timestamp the new status requires. Combined with
-- ListResolvedTicketsBefore's now-added `status_id = $2` filter, a row that
-- still carries a stale resolved_at but sits in some other status would
-- simply stop being visible to the auto-close sweep at all — which is
-- correct going forward, but leaves the existing bad rows sitting there
-- looking like they were never resolved, or (for a Closed ticket with no
-- closed_at) not fully closed.

-- #230: this migration's destructive statements below match rows by STATUS
-- NAME ('New', 'Resolved', 'Closed'), but the admin API blocks deactivating a
-- system status without blocking a RENAME of one (handleUpdateStatus /
-- SaveStatus). If an admin renames one between deploy and this migration
-- actually running on an upgrade, the name-based exclusion below silently
-- stops matching the renamed status, reproducing #208's original data-loss
-- bug through a different path — irreversibly, since the down migration is a
-- no-op.
--
-- Guard against that here: verify all three system status names this
-- codebase's LoadSystemStatuses (ticket/service.go) depends on still exist,
-- exactly once each, before running anything destructive, and abort loudly
-- rather than silently misclassify rows if they don't. A renamed status
-- already breaks LoadSystemStatuses at the app's next restart — which is
-- exactly when this migration would run on an upgrade — so this is belt and
-- suspenders, not the only defense, but it is cheap and this is an
-- irreversible data migration.
--
-- #232: counted by kind = 'system' as well as by name, not by name alone.
-- The status name column is UNIQUE, so renaming the real system-kind
-- "Closed" status frees that name for a NEW custom-kind status to reuse —
-- count(*) = 1 by bare name still passes in that shape (exactly one row is
-- named "Closed"; it is simply the wrong one), and the destructive
-- statements below would then silently operate on the newly-created custom
-- status instead of aborting. kind is exactly the column migration 000001
-- seeded for this discriminator; a status renamed away from "Closed" is no
-- longer kind = 'system' AND name = 'Closed', so this guard now correctly
-- finds zero such rows and aborts, rather than one row of the wrong kind.
--
-- #237: the check on 'New' below is no longer load-bearing for any statement
-- in this file — after #237's fix, none of R1-R4 singles out 'New' any more
-- (the old, narrower R1 depended on it only by accident, which is itself
-- what #237 was filed against). It is kept anyway, unchanged, purely as a
-- mirror of LoadSystemStatuses, which fails the app's own startup on exactly
-- this shape — if this guard were dropped, an upgrade could apply this
-- migration on a database where 'New' is already broken in a way the running
-- app would refuse to start against.
DO $$
DECLARE
    n int;
BEGIN
    SELECT count(*) INTO n FROM statuses WHERE name = 'New' AND kind = 'system';
    IF n <> 1 THEN
        RAISE EXCEPTION 'migration 000028: expected exactly one SYSTEM status named ''New'', found %; a system status may have been renamed (and its name possibly reused by a new custom status) — aborting rather than risk misclassifying rows by name (#230, #232)', n;
    END IF;

    SELECT count(*) INTO n FROM statuses WHERE name = 'Resolved' AND kind = 'system';
    IF n <> 1 THEN
        RAISE EXCEPTION 'migration 000028: expected exactly one SYSTEM status named ''Resolved'', found %; a system status may have been renamed (and its name possibly reused by a new custom status) — aborting rather than risk misclassifying rows by name (#230, #232)', n;
    END IF;

    SELECT count(*) INTO n FROM statuses WHERE name = 'Closed' AND kind = 'system';
    IF n <> 1 THEN
        RAISE EXCEPTION 'migration 000028: expected exactly one SYSTEM status named ''Closed'', found %; a system status may have been renamed (and its name possibly reused by a new custom status) — aborting rather than risk misclassifying rows by name (#230, #232)', n;
    END IF;
END $$;

-- ============================================================
-- Phase A: repair the tickets table so every row matches the invariant
-- table above. R1-R4 work on disjoint sets of rows (partitioned by which
-- side of "is this ticket currently in system Resolved / system Closed" they
-- match), so their relative order does not matter — but all four must run
-- before phase B below, which trusts their result.
-- ============================================================

-- R1 (#237): no ticket outside system Resolved/Closed may carry resolved_at.
-- This is the invariant table's default-branch rule, matching every status
-- except the two system terminal ones — kind only narrows WHICH statuses are
-- excluded, it does not narrow the set being repaired down to 'New'. The old
-- statement here matched by `s.name NOT IN ('Resolved', 'Closed')` scoped to
-- kind = 'system', which — because every CUSTOM status also has
-- kind <> 'system', hence also fails that AND — only ever matched the
-- system 'New' row, so 'In Progress', 'Pending', and any admin-created
-- custom status kept a stale resolved_at forever. The #232 protection is
-- unaffected: the guard above has already confirmed that exactly one system
-- Resolved and one system Closed exist, and the name column is UNIQUE, so no
-- custom status can be reusing either name.
UPDATE tickets t
SET resolved_at = NULL
FROM statuses s
WHERE t.status_id = s.id
  AND NOT (s.kind = 'system' AND s.name IN ('Resolved', 'Closed'))
  AND t.resolved_at IS NOT NULL;

-- R2 (#237/#238, new — see the file-level note below the guard for how this
-- was found): a ticket sitting in system Resolved must carry resolved_at.
-- Pre-#102 UpdateStatus (see that commit) could set StatusID alone, leaving a
-- ticket resolved through it with a NULL resolved_at — invisible to
-- ListResolvedTicketsBefore (which requires resolved_at < cutoff) and read as
-- "permanently resolved" by lifecycleAllowsReply (ResolvedAt == nil). Left
-- unrepaired it also silently defeats R3/S1 below: once R3 clears its stale
-- closed_at, S1's COALESCE(t.resolved_at, t.closed_at) has nothing to read
-- and the legacy sweep stamps a false breach dated at the sweep anyway.
--
-- Recovered from the start of the ticket's CURRENT Resolved stint, not
-- now(): the subquery finds the most recent history row that both enters
-- Resolved from a DIFFERENT status (so a Resolved→Resolved duplicate row,
-- which pre-#102 Resolve appended on a re-resolve, is skipped) and has no
-- later row leaving Resolved (so an earlier stint's entry, before the ticket
-- was reopened and resolved again, is skipped too). If no such row exists —
-- no history at all, or a door moved the ticket into Resolved without
-- writing history — the second condition can never be satisfied and the
-- subquery returns NULL, and COALESCE falls back to t.updated_at, which
-- pre-#102 UpdateStatus set to time.Now() in the same transaction as the
-- history write (or, absent any history, is still the best available
-- estimate of the transition instant — see the file-level note on this
-- fallback's known limits). now() is never used: t.updated_at is
-- NOT NULL DEFAULT now() per migration 000001, so that fallback can never be
-- reached.
UPDATE tickets t
SET resolved_at = COALESCE(
        (SELECT max(h.created_at)
           FROM ticket_status_history h
          WHERE h.ticket_id = t.id
            AND h.to_status_id = s.id
            AND h.from_status_id IS DISTINCT FROM s.id
            AND NOT EXISTS (
                    SELECT 1
                      FROM ticket_status_history later
                     WHERE later.ticket_id = t.id
                       AND later.created_at > h.created_at
                       AND later.to_status_id <> s.id)),
        t.updated_at)
FROM statuses s
WHERE t.status_id = s.id
  AND s.kind = 'system'
  AND s.name = 'Resolved'
  AND t.resolved_at IS NULL;

-- R3 (#239): no ticket outside system Closed may carry closed_at — the
-- invariant table's default-branch rule for closed_at, exactly mirroring R1
-- for resolved_at. This subsumes the old Resolved-only statement that used
-- to sit here (`s.name = 'Resolved' AND t.closed_at IS NOT NULL`, clearing a
-- stale closed_at left over from resolving a previously-Closed ticket before
-- resolveInTx's own fix), so that statement is deleted rather than kept
-- alongside a now-overlapping rule. A ticket moved Closed→New or
-- Closed→In Progress by pre-fix UpdateStatus without clearing closed_at now
-- gets it cleared too: ListSLABreachCandidates (t.closed_at IS NULL) and
-- ListResolvedTicketsBefore see it again once it is genuinely reopened, and
-- both guest-token lookups (queries/guest_tokens.sql) work again. After
-- R1-R4 finish, "closed_at is set" means exactly "the ticket is in system
-- Closed" — see S1 below, which no longer needs any argument for tolerating
-- a stale closed_at, because there no longer is one.
UPDATE tickets t
SET closed_at = NULL
FROM statuses s
WHERE t.status_id = s.id
  AND NOT (s.kind = 'system' AND s.name = 'Closed')
  AND t.closed_at IS NOT NULL;

-- R4 (#241): a ticket sitting in system Closed must carry closed_at.
-- Recovered from the start of the ticket's current Closed stint using the
-- SAME rule as R2 above — NOT now(), which the very first version of this
-- statement used, and NOT a plain `max(created_at) WHERE to_status_id =
-- Closed`, which has two edge cases the from/NOT EXISTS form both handle:
-- pre-#102 Close could append a Closed→Closed duplicate history row on a
-- re-close (picking a later instant than the true close), and a door that
-- moved the ticket OUT of Closed without writing history would otherwise
-- return an earlier stint's close instant instead of falling back to
-- updated_at. See R2's comment for the full rule and the fallback's known
-- limits.
UPDATE tickets t
SET closed_at = COALESCE(
        (SELECT max(h.created_at)
           FROM ticket_status_history h
          WHERE h.ticket_id = t.id
            AND h.to_status_id = s.id
            AND h.from_status_id IS DISTINCT FROM s.id
            AND NOT EXISTS (
                    SELECT 1
                      FROM ticket_status_history later
                     WHERE later.ticket_id = t.id
                       AND later.created_at > h.created_at
                       AND later.to_status_id <> s.id)),
        t.updated_at)
FROM statuses s
WHERE t.status_id = s.id
  AND s.kind = 'system'
  AND s.name = 'Closed'
  AND t.closed_at IS NULL;

-- ============================================================
-- Phase B (#231: moved here from migration 000027_sla_frozen_elapsed.up.sql
-- — see that file's own note): the #226(a)/(b) SLA backfill for pre-existing
-- sla_records rows that violate the OLD rules #219/#220 fixed in code but not
-- in already-written data. Must run AFTER phase A above, which is what
-- guarantees S1's COALESCE below is never NULL for the rows it selects.
-- ============================================================

-- S1 (#238): backfill sla_records.resolved_at for every ticket currently in
-- system Resolved or Closed whose SLA record does not have one yet. Before
-- this, the key was `name = 'Closed'` alone (see #231's note), so a ticket
-- resolved under a pre-v1.2.0 build and still sitting in Resolved at upgrade
-- was skipped here — the very next breach sweep would then read its NULL
-- sla_records.resolved_at as "not yet resolved" and stamp a false breach
-- dated at the sweep, which nothing could ever undo (RecordResolved's no-op
-- guard means a real resolution never reaches this record again). Restoring
-- 'Resolved' to the key alone would still have been a no-op in one shape — a
-- Resolved ticket whose OWN t.resolved_at was itself NULL, the pre-#102
-- UpdateStatus shape — the COALESCE below would have read
-- COALESCE(NULL, closed_at), and closed_at is NULL for a Resolved ticket once
-- R3 has run. R2 above closes that gap: after phase A, a system-Resolved
-- ticket always has resolved_at, and a system-Closed ticket has resolved_at
-- (its own, if it has one) or closed_at (guaranteed by R4) — the COALESCE
-- here can no longer be NULL for any row this statement selects. Kept as a
-- status key rather than dropped for `r.resolved_at IS NULL` alone (which
-- would now be sufficient) as defence in depth: a row that is NOT currently
-- in one of the two terminal statuses should not be having a resolution
-- fabricated for it here at all, whatever COALESCE would compute.
UPDATE sla_records r
SET resolved_at = COALESCE(t.resolved_at, t.closed_at)
FROM tickets t
JOIN statuses s ON s.id = t.status_id
WHERE t.id = r.ticket_id
  AND r.resolved_at IS NULL
  AND s.kind = 'system'
  AND s.name IN ('Resolved', 'Closed');

-- S2: freeze resolution_elapsed_at_met_seconds wherever it is still missing —
-- exactly the rows S1 just gave a resolved_at to; every other row either
-- already carries one (migration 000027's own backfill already froze it) or
-- still has no resolved_at at all. Same formula as that earlier pass, now
-- with a value to compute it from. #240: this statement ONLY freezes; it no
-- longer also decides a breach in the same UPDATE (see S3 below for why that
-- split matters).
UPDATE sla_records r
SET resolution_elapsed_at_met_seconds = GREATEST(
        0,
        EXTRACT(EPOCH FROM (r.resolved_at - t.created_at))::bigint - t.sla_paused_seconds
    )
FROM tickets t
WHERE t.id = r.ticket_id
  AND r.resolved_at IS NOT NULL
  AND r.resolution_elapsed_at_met_seconds IS NULL;

-- S3 (#235 + #240): stamp a resolution breach on EVERY record whose frozen
-- elapsed reading is already past the policy's resolution target and that
-- has no breach stamp yet — whichever migration (this one, or 000027's own
-- earlier best-effort pass) is the one that froze it. Same strict `>` and the
-- same stamp instant (the resolution instant itself) as SetSLAResolved.
--
-- Before this, each freeze statement ALSO stamped its own breach, gated on
-- `elapsed IS NULL` — so the breach decision was tied to whether THAT
-- statement had just written the frozen value, and a row 000027 had frozen
-- on an earlier upgrade (or one this file's own S2 skips because it is
-- already frozen) could carry a genuinely late frozen reading with a
-- permanently NULL breach column, because nothing revisited it once frozen.
-- Splitting freeze and stamp into separate statements fixes this: S3 decides
-- purely from the STORED frozen seconds, reaching every row regardless of
-- which pass froze it. This cannot produce a FALSE stamp: 000027's
-- best-effort frozen value can only UNDERSTATE the true elapsed time (the
-- ticket's current sla_paused_seconds is at least what it was when the
-- target was met, and the pending clip only ever subtracts more), so this
-- can at worst miss a breach it should have caught, never invent one that
-- did not happen. An existing stamp — from an earlier sweep, or from
-- RecordResolved itself — is preserved by the `IS NULL` guard: a stamp is a
-- fact about what happened and is never cleared here.
--
-- Single FROM item (sla_policies alone): the old comma-join workaround for
-- "the UPDATE target's own alias cannot appear in a JOIN...ON" is no longer
-- needed now that this statement does not also need to join tickets.
UPDATE sla_records r
SET resolution_breached_at = r.resolved_at
FROM sla_policies p
WHERE p.id = r.policy_id
  AND r.resolved_at IS NOT NULL
  AND r.resolution_breached_at IS NULL
  AND r.resolution_elapsed_at_met_seconds > p.resolution_target_min::bigint * 60;

-- S4 (#226(a)): a ticket resolved without ever getting a prior staff reply
-- was allowed under the OLD rules, before #219 taught RecordResolved that "a
-- resolution is a response in every practical sense." Left NULL,
-- IsResponseBreached computes a live Elapsed(t, now) against a target that
-- was, in fact, met the moment the ticket resolved — reading a promptly
-- resolved ticket as a permanent response breach the instant wall-clock time
-- passes the response target. Runs after S1 (not before): a ticket that was
-- both closed-without-resolving and never separately responded to needs its
-- resolved_at filled in first, so this pass has a resolution instant to
-- treat as the response too — first_response_at = resolved_at, exactly
-- #219's rule, whichever door (a real resolution, or S1's backfill) supplied
-- it. No ticket-status key needed here, and no `FROM tickets t` join either
-- (the old join was never read from): this depends only on sla_records' own
-- columns.
UPDATE sla_records r
SET first_response_at = r.resolved_at
WHERE r.resolved_at IS NOT NULL
  AND r.first_response_at IS NULL;

-- S5: freeze response_elapsed_at_met_seconds wherever it is still missing,
-- using the same pending-aware clip as migration 000027's own
-- response_elapsed_at_met_seconds pass (#222) — first response and
-- resolution landed at the same instant for exactly the rows S4 just touched
-- (every other row either already has one frozen, or still has no
-- first_response_at at all).
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

-- S6 (#235 + #240): the response-side twin of S3 — see that statement's
-- comment for the reasoning, which applies identically here.
UPDATE sla_records r
SET response_breached_at = r.first_response_at
FROM sla_policies p
WHERE p.id = r.policy_id
  AND r.first_response_at IS NOT NULL
  AND r.response_breached_at IS NULL
  AND r.response_elapsed_at_met_seconds > p.response_target_min::bigint * 60;
