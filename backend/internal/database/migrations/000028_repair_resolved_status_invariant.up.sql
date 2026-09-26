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

-- #248: freeze the statuses table for the rest of this file. golang-migrate
-- runs this file as one implicit READ COMMITTED transaction, so every
-- statement below takes a fresh snapshot; without this, a rename committed
-- by a still-running old app instance after the guard below passed would be
-- visible to C1 and R1-R4's name-keyed joins (#208 through a timing path).
-- SHARE conflicts with the ROW EXCLUSIVE lock any UPDATE/INSERT/DELETE on
-- statuses takes, so: (1) this waits for any in-flight rename to commit or
-- abort, (2) the guard's own snapshot, taken AFTER the lock is granted, sees
-- it, and (3) no rename can commit until this transaction ends. Taken BEFORE
-- the guard, not after: a lock taken after would leave the window between
-- the guard's snapshot and the lock open. Top-level rather than inside the
-- DO block so that running this file statement-by-statement in autocommit
-- (x-multi-statement) fails loudly ("LOCK TABLE can only be used in
-- transaction blocks") instead of silently releasing the lock per statement.
LOCK TABLE statuses IN SHARE MODE;

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
-- LIMITS: where this file's recovered instants come from, and which way
-- each can be wrong. Referenced by name from C1, C2, R2, R4, S1 and S3.
--
-- Two sources:
--
-- 1. FACTS: a value some door wrote at the instant it describes.
--    tickets.resolved_at is only ever written by a resolve, at that instant.
--    tickets.closed_at is only ever written by a close. A
--    ticket_status_history row's created_at is the instant of the change it
--    records. A fact can be a LATER occurrence of the event than the first
--    one (pre-#102 Resolve and Close re-stamped on every call), but never a
--    moment the event did not happen, so the earliest available fact is
--    exact whenever the first occurrence was recorded anywhere.
--
-- 2. THE updated_at FALLBACK, used by R2/R4 only when no fact exists. It is
--    an UPPER bound on the transition, never an estimate that can err either
--    way: every later write through the ticket row (in current code: every
--    status door, Assign, UpdateCTI) moves it forward and nothing moves it
--    back. It is reachable in real data: v1.0.0 through v1.1.0 wrote the
--    history row AFTER the ticket update, outside any transaction, and
--    discarded the history write's error (recordStatusChange), so a
--    committed status change with no history row is a released shape.
--    v1.1.1 (#86) moved that write into the ticket's transaction, which
--    stopped new cases but repaired none.
--
-- Which direction is safe depends on the reader:
--  - tickets.resolved_at / closed_at (R2, R4): a later instant keeps the
--    reopen window open longer and delays auto-close. That is the safe
--    direction for those readers, so R2/R4 keep the fallback.
--  - sla_records (phase B): a later instant OVERSTATES elapsed time, the
--    opposite of the "can only understate, so at worst miss a breach, never
--    invent one" argument S3 makes for 000027's frozen values. An instant
--    from the fallback is therefore recorded and frozen (the ticket needs
--    SOME resolution instant, or the next breach sweep stamps one dated at
--    the sweep, #238), but it never produces a breach stamp: a stamp is a
--    fact about what happened, and this is not one. C2 marks these rows
--    estimated, and S3/S6 read the mark.
--
-- Accepted residual (#246): an estimated row's resolution_elapsed_at_met_seconds
-- (and, when S4 copies the same instant, response_elapsed_at_met_seconds)
-- stays NULL forever — S1 below deliberately never freezes a number for a
-- row it marks estimated. That NULL is not "missing"; it is now the durable
-- marker that tells a later run which rows were only ever estimated, so it
-- must never be filled in later. The hazard this file must never
-- reintroduce is a pass that freezes a number into that NULL: the moment a
-- met target has BOTH a timestamp and a frozen elapsed reading, S3/S6 read
-- it as a fact and stamp a breach from it, which is exactly the
-- false-stamp-on-rerun bug #246 was filed against.
--
-- That marker's durability has a boundary, and #249 is what found it. It
-- survives re-running 000028 up alone against data this file already
-- repaired, and it survives a 028-only down (which is a no-op — see that
-- file — so "down then up" replays this exact file against unchanged data,
-- which is what TestMigration_028SecondRunChangesNothing exercises). It does
-- NOT survive a rewind that goes down through 000027 itself: 000027's down
-- drops the response_elapsed_at_met_seconds/resolution_elapsed_at_met_seconds
-- columns entirely, and dropping them destroys the ONLY place the
-- fact/estimate distinction was recorded — there is no SQL fix for this in
-- either file, because the information the fix would need is gone, not
-- merely unread. Once 000027's up recreates the columns, its own backfill
-- unconditionally re-freezes elapsed from whatever sla_records.resolved_at
-- (and first_response_at) already hold, with no way left to tell that one of
-- those instants came from the updated_at fallback rather than a fact. For a
-- row this file previously marked estimated, that re-frozen number is an
-- upper bound and can be late, and it is now indistinguishable from a fact:
-- the next 000028 up sees a non-NULL frozen elapsed reading past target and
-- S3/S6 stamp a breach from it — permanently, for a resolution that was
-- never actually confirmed. TestMigration_RewindThrough000027LosesEstimatedMarkerAndReStamps
-- pins this exact sequence (028 up, 028 down, 027 down, 027 up, 028 up) and
-- asserts the re-stamp happens, so this is a documented, accepted trade-off —
-- not a bug to fix later, and not something a schema column should be added
-- to prevent (that would be v2+ scope DESIGN.md does not describe).
--
-- The residual this leaves: targetStatus (status.go) reads MetAt set but
-- frozenSeconds NULL as "no frozen number yet" and falls back to a live
-- Elapsed(t, *MetAt) — for an estimated row that recompute uses the ticket's
-- CURRENT sla_paused_seconds, which only grows. So if an estimated ticket is
-- reopened after this migration runs and later passes through Pending
-- again, its live reading can drift toward green as more pause time
-- accumulates, breaking DESIGN.md's "once a target's timestamp is set, that
-- target's indicator stops updating" for estimated rows only. The drift is
-- always in the understating direction, applied to a reading that was
-- already an upper bound (see above), and it can never produce a breach
-- stamp: IsResolutionBreached/IsResponseBreached fall back to the identical
-- live recompute for the same reason, and the sweep never revisits a record
-- with both resolved_at/first_response_at set (ListSLABreachCandidates
-- requires one of them NULL). Accepted rather than fixed with a new schema
-- column, which DESIGN.md does not describe.
-- ============================================================

-- ============================================================
-- Phase A: capture, repair, capture again.
--
-- C1 runs FIRST: R1 destroys a fact it needs (#244), and R2/R4 write
-- fallback values that must never be mistaken for facts. R1-R4 then repair
-- the tickets table. C2 runs LAST, after R2/R4, because its job is to pick
-- up exactly the instants those two took from the updated_at fallback.
--
-- R1-R4's relative order does not matter, and the reason is COLUMNS, not
-- rows. R1/R2 read and write only resolved_at, and R3/R4 read and write
-- only closed_at (all four also read status/history/updated_at, which none
-- of them writes). Within each pair the row sets are disjoint (R1: outside
-- system Resolved/Closed, R2: system Resolved, R3: outside system Closed,
-- R4: system Closed). Across pairs they DO overlap: every system Resolved
-- row matches both R2 and R3. An edit that makes one pair touch the other
-- pair's column breaks this argument.
-- ============================================================

-- Scratch space for phase B's S1, dropped at the end of the file (#246: S3
-- and S6 used to read `estimated` from here directly; they now read it off
-- sla_records' own frozen-elapsed columns instead, which survive a second
-- run — this table does not). A plain temp table with an explicit DROP
-- rather than ON COMMIT DROP: golang-migrate runs this file as one implicit
-- transaction, and the migration tests run it statement by statement inside
-- an outer transaction, and an explicit DROP is correct under both.
CREATE TEMP TABLE m28_sla_resolution (
    ticket_id   UUID        PRIMARY KEY,
    resolved_at TIMESTAMPTZ NOT NULL,
    estimated   BOOLEAN     NOT NULL
);

-- C1 (#242, #244): capture every SLA resolution instant a FACT supports
-- (see LIMITS), before anything below rewrites the columns it reads. Only
-- tickets whose sla_records row still has no resolved_at are captured.
--
-- The instant is the ticket's FIRST resolution, because that is what the
-- SLA record holds when resolutions are recorded as they happen:
-- RecordResolved no-ops once one exists, and a reopen never clears it
-- (DESIGN.md: "a reopened ticket is not treated as a new SLA clock").
-- Before v1.2.0 nothing recorded a resolution at all, so every legacy
-- ticket ever resolved arrives here with a NULL.
--
-- First arm, any current status: the earliest of tickets.resolved_at and
-- every history row ENTERING Resolved from another status. No status key,
-- on purpose:
--  - #242: a ticket PATCHed into Resolved without a stamp (pre-#102) and
--    later Closed has resolved_at NULL, and R2 never sees it because it is
--    not in Resolved. Its Resolved stint is in history. Without this, S1
--    fell through to the later close instant and stamped false breaches.
--  - #244: a ticket moved off Resolved by pre-#102 UpdateStatus still
--    carries the resolved_at its resolve stamped. R1 clears it, which is
--    right for the tickets table, but it is a real resolution and the SLA
--    record must keep it. The same holds when a reply or Reopen cleared
--    resolved_at but history still records the resolve.
--  - a ticket resolved, reopened and resolved again carries only its
--    LATEST resolve in resolved_at. The earlier stint is the one the SLA
--    record would have kept.
-- A Resolved->Resolved duplicate row (pre-#102 Resolve appended one on a
-- re-resolve) is skipped: it records "still resolved", not when the
-- resolve happened. LEAST ignores NULLs.
--
-- Second arm (#247): close facts are included only for a ticket CURRENTLY in
-- system Closed, and they are COMPARED against the resolve facts via a
-- single flat LEAST, not consulted only when the first arm found nothing.
-- C1's rule is the ticket's first resolution, and for a ticket still Closed
-- a close counts as one (#220 applied retroactively, exactly where #226(b)
-- needs it: a ticket still Closed is skipped by the breach sweep and would
-- otherwise show an open, ever-growing resolution target forever). The old
-- COALESCE consulted the close arm only when the resolve arm was NULL, so
-- for a ticket resolved late, reopened, and closed again, adding a NEW
-- on-time resolve fact to history could flip an already-correct on-time
-- close-based record into a false late breach the next time this file ran —
-- LEAST picks whichever fact is EARLIEST instead, which for a ticket that
-- passed through Closed before ever resolving is the close, and for one
-- that resolved before its (first) close is the resolve, matching #242's
-- "first resolution" rule either way.
--
-- LEAST is NULL only when every argument is NULL, exactly the case the old
-- COALESCE was also NULL in, so which tickets get a row here is unchanged
-- (#247 does not affect C2). A reopened ticket never satisfies
-- `t.status_id = cs.id`, so its close-arm arguments are always NULL and it
-- keeps using only the resolve arm — the same guarantee the old CASE gave,
-- for the same reason (#239, #244), and it is what keeps a Resolved
-- ticket's own stale closed_at out of its resolve (#238).
INSERT INTO m28_sla_resolution (ticket_id, resolved_at, estimated)
SELECT f.ticket_id, f.at, false
  FROM (SELECT t.id AS ticket_id,
               LEAST(
                   t.resolved_at,
                   (SELECT min(h.created_at)
                      FROM ticket_status_history h
                     WHERE h.ticket_id = t.id
                       AND h.to_status_id = rs.id
                       AND h.from_status_id IS DISTINCT FROM rs.id),
                   CASE WHEN t.status_id = cs.id THEN t.closed_at END,
                   CASE WHEN t.status_id = cs.id THEN
                       (SELECT min(h.created_at)
                          FROM ticket_status_history h
                         WHERE h.ticket_id = t.id
                           AND h.to_status_id = cs.id
                           AND h.from_status_id IS DISTINCT FROM cs.id)
                   END) AS at
          FROM tickets t
          JOIN sla_records r ON r.ticket_id = t.id
         CROSS JOIN statuses rs
         CROSS JOIN statuses cs
         WHERE r.resolved_at IS NULL
           AND rs.kind = 'system' AND rs.name = 'Resolved'
           AND cs.kind = 'system' AND cs.name = 'Closed') f
 WHERE f.at IS NOT NULL;

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
--
-- C1 above has already captured the SLA resolution this clears, so a
-- genuine legacy resolution on a reopened ticket survives in sla_records
-- (#244).
UPDATE tickets t
SET resolved_at = NULL
FROM statuses s
WHERE t.status_id = s.id
  AND NOT (s.kind = 'system' AND s.name IN ('Resolved', 'Closed'))
  AND t.resolved_at IS NOT NULL;

-- R2 (#237/#238; see LIMITS below the guard for the updated_at fallback this
-- uses and which direction it can be wrong): a ticket sitting in system
-- Resolved must carry resolved_at.
-- Pre-#102 UpdateStatus (see that commit) could set StatusID alone, leaving a
-- ticket resolved through it with a NULL resolved_at — invisible to
-- ListResolvedTicketsBefore (which requires resolved_at < cutoff) and read as
-- "permanently resolved" by lifecycleAllowsReply (ResolvedAt == nil). Left
-- unrepaired it also silently defeats R3/S1 below: once R3 clears its stale
-- closed_at, S1's COALESCE(t.resolved_at, t.closed_at) has nothing to read
-- and the legacy sweep stamps a false breach dated at the sweep anyway.
--
-- Recovered from the start of the ticket's CURRENT Resolved stint, not
-- now(), and not its first resolution either (C1 uses that, for the SLA
-- record): this column drives the reopen window and auto-close, which run
-- from the resolution the ticket is sitting in now. The subquery finds the
-- most recent history row that both enters Resolved from a DIFFERENT status
-- (so a Resolved→Resolved duplicate row, which pre-#102 Resolve appended on
-- a re-resolve, is skipped) and has no later row leaving Resolved (so an
-- earlier stint's entry, before the ticket was reopened and resolved again,
-- is skipped too). If no such row exists — no history at all, or a door
-- moved the ticket into Resolved without writing history — the second
-- condition can never be satisfied and the subquery returns NULL, and
-- COALESCE falls back to t.updated_at, an upper bound on the transition. See
-- LIMITS for why that is the safe direction for this column, and why phase B
-- never stamps a breach from it. now() is never used: t.updated_at is
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
-- both guest-token lookups (queries/guest_tokens.sql) work again.
--
-- After this, "closed_at is set" means exactly "the ticket is in system
-- Closed". C1 read closed_at before this ran, and only for tickets
-- currently Closed, which this statement never touches.
UPDATE tickets t
SET closed_at = NULL
FROM statuses s
WHERE t.status_id = s.id
  AND NOT (s.kind = 'system' AND s.name = 'Closed')
  AND t.closed_at IS NOT NULL;

-- R4 (#241): a ticket sitting in system Closed must carry closed_at,
-- recovered from the start of its CURRENT Closed stint by the same rule as
-- R2 (duplicate Closed->Closed rows skipped, an earlier stint never
-- resurrected when a later row leaves Closed), falling back to
-- t.updated_at. See LIMITS for that fallback.
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

-- C2 (#243): every ticket currently in system Resolved or Closed that C1
-- found no fact for. These are exactly the rows whose resolved_at (R2) or
-- closed_at (R4) came from the updated_at fallback: R2's and R4's history
-- lookups only match rows C1's lookups also match, and C1 already took
-- every stored resolved_at, plus closed_at for Closed tickets. They still
-- need an SLA resolution instant (otherwise a Resolved one gets a breach
-- stamped at the next sweep, #238, and a Closed one shows an open
-- resolution target forever, #226(b)), so one is recorded here, marked
-- estimated. The `estimated` column is read only by S1 below (#246): it is
-- what tells S1 to write resolved_at without a frozen elapsed number,
-- leaving the NULL that is itself the durable marker S3/S5/S6 key off later
-- (see LIMITS). The column's NOT NULL turns "phase A leaves COALESCE
-- non-NULL here" into a checked assertion: a row that breaks it aborts the
-- migration instead of silently skipping the row.
INSERT INTO m28_sla_resolution (ticket_id, resolved_at, estimated)
SELECT t.id, COALESCE(t.resolved_at, t.closed_at), true
  FROM tickets t
  JOIN statuses s ON s.id = t.status_id
  JOIN sla_records r ON r.ticket_id = t.id
 WHERE r.resolved_at IS NULL
   AND s.kind = 'system'
   AND s.name IN ('Resolved', 'Closed')
   AND NOT EXISTS (
           SELECT 1 FROM m28_sla_resolution c WHERE c.ticket_id = t.id);

-- ============================================================
-- Phase B (#231: moved here from 000027): the #226(a)/(b) SLA backfill.
-- Reads only phase A's capture table for resolution instants, never the
-- repaired tickets columns directly.
-- ============================================================

-- S1 (#238, #242, #243, #244, #246): record the captured resolution instant,
-- and freeze its elapsed-toward-target number IN THE SAME STATEMENT — but
-- only for a row C1/C2 did NOT mark estimated. This absorbs what used to be
-- a separate S2 freeze pass; the two are folded together for the same reason
-- #228 folded a fact write with its own breach decision: the fact and the
-- number derived from it must land in one atomic write, or a second run of
-- this file cannot tell "already handled" apart from "still needs handling"
-- purely from resolved_at (every row has that; only a NON-estimated row
-- ever gets a frozen number). Which tickets get a row here, and from which
-- instant, is decided entirely by C1/C2. This statement no longer carries
-- the status key it used to (first 'Closed' alone, #231, then
-- 'Resolved'/'Closed', #238), because C1 deliberately includes tickets that
-- are no longer terminal (#244). Every row C2 added is currently terminal,
-- and every row C1 added rests on a real resolve or close, so nothing is
-- invented for a ticket that was never resolved. `r.resolved_at IS NULL` is
-- redundant with the capture's own filter and is kept as the same
-- first-writer-wins guard SetSLAResolved uses.
--
-- #246: this is the fix for the second-run bug. The old, separate S2 froze
-- resolution_elapsed_at_met_seconds for every row with resolved_at set and
-- the column still NULL, with no reference to what THIS run's capture table
-- held — so on a second run, every row the FIRST run had deliberately left
-- unfrozen (an estimated row, per LIMITS) had resolved_at already set from
-- run one, and S2 would freeze it anyway, handing S3 a stamped-looking
-- number to stamp a false breach from. Tying the freeze to
-- `NOT c.estimated` — read from THIS run's own capture, which is empty on
-- every later run — means a later run freezes nothing new: an already-set
-- resolved_at fails this statement's `r.resolved_at IS NULL` guard before
-- the CASE is ever reached, and a row this run's C1/C2 did add either was
-- never estimated (frozen once, correctly) or was estimated (its
-- resolution_elapsed_at_met_seconds column is the durable marker future runs
-- read via that same NULL check, not via the temp table, which is gone by
-- the time the next migrate run starts). See LIMITS for why leaving it NULL,
-- rather than filling it with an estimate, is the fix.
--
-- For a reopened ticket (#244) whose resolve fact IS used (not estimated),
-- the ticket's current sla_paused_seconds includes pauses after the
-- resolution, which can only understate, the same direction as 000027's own
-- backfill.
--
-- #250: clipped by the same pending-time CASE that S5 below applies on the
-- response side, and that live sla.Elapsed (sla.go) always applies when
-- t.PendingSince is set. C1 (#244) captures a resolution fact for a ticket
-- that is not currently terminal, including one now sitting in Pending, so
-- this can no longer assume "resolved means not Pending" the way 000027's own
-- original resolution-side pass (#222) did. Without the clip, a captured
-- instant AFTER pending_since overstates elapsed relative to the live
-- formula and can stamp a breach S3 would not otherwise stamp — the one
-- direction S3's own comment says this file must never produce.
UPDATE sla_records r
SET resolved_at = c.resolved_at,
    resolution_elapsed_at_met_seconds = CASE
        WHEN NOT c.estimated THEN GREATEST(
            0,
            EXTRACT(EPOCH FROM (c.resolved_at - t.created_at))::bigint - t.sla_paused_seconds
                - CASE
                      WHEN t.pending_since IS NOT NULL
                      THEN GREATEST(0, EXTRACT(EPOCH FROM (c.resolved_at - t.pending_since)))::bigint
                      ELSE 0
                  END)
    END
FROM m28_sla_resolution c
JOIN tickets t ON t.id = c.ticket_id
WHERE c.ticket_id = r.ticket_id
  AND r.resolved_at IS NULL;

-- S3 (#235, #240, #243, #246): stamp a resolution breach on every record
-- whose frozen elapsed reading is past the policy's resolution target and
-- that has no stamp yet, whichever pass froze it. No estimated gate is
-- needed here any more: S1 above never freezes a number for an estimated
-- row, so `resolution_elapsed_at_met_seconds > target` is comparing against
-- NULL for exactly those rows, and `NULL > x` is never true in Postgres —
-- three-valued logic excludes them on its own, the same way the WHERE
-- clause already relies on for every other NULL column here. Same strict
-- `>` and same stamp instant as SetSLAResolved.
--
-- Before this, each freeze statement ALSO stamped its own breach, gated on
-- `elapsed IS NULL` — so the breach decision was tied to whether THAT
-- statement had just written the frozen value, and a row 000027 had frozen
-- on an earlier upgrade could carry a genuinely late frozen reading with a
-- permanently NULL breach column, because nothing revisited it once frozen.
-- Splitting freeze and stamp into separate statements fixes this: S3 decides
-- purely from the STORED frozen seconds, reaching every row regardless of
-- which pass froze it.
--
-- Why this cannot produce a FALSE stamp, by where the frozen value came
-- from: 000027's best-effort pass can only UNDERSTATE (the ticket's current
-- sla_paused_seconds is at least what it was when the target was met). S1
-- over a C1 fact is exact up to that same pause approximation, which only
-- understates. S1 never freezes a number over a C2 estimate at all (see
-- above and LIMITS), so there is no OVERSTATED number for this statement to
-- ever read. At worst this misses a breach. It never invents one. An
-- existing stamp is preserved by the IS NULL guard: a stamp is a fact and is
-- never cleared here.
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

-- S5 (#246): freeze response_elapsed_at_met_seconds wherever it is still
-- missing, using the same pending-aware clip as migration 000027's own
-- response_elapsed_at_met_seconds pass (#222) — first response and
-- resolution landed at the same instant for exactly the rows S4 just touched
-- (every other row either already has one frozen, or still has no
-- first_response_at at all).
--
-- The trailing NOT (...) is this statement's own durable gate, the response
-- side's twin of S1's `NOT c.estimated`: it excludes exactly the rows where
-- S4 just copied an ESTIMATED resolution instant into first_response_at —
-- recognised, now that the temp table is gone by the time a later run
-- starts, by the row's own stored columns: resolved_at set,
-- resolution_elapsed_at_met_seconds still NULL (S1's marker for "this
-- resolution was estimated"), and first_response_at equal to that same
-- resolved_at (S4's copy, not an independent fact). Without this, a second
-- run would freeze response_elapsed_at_met_seconds for those rows from
-- whatever first_response_at/resolved_at already carry — harmless in
-- isolation, but it would then hand S6 a frozen number to stamp a false
-- response breach from, the response-side mirror of the bug S1's gate
-- fixes on the resolution side.
--
-- `r.resolved_at IS NOT NULL` in the gate is required, not redundant: without
-- it, a row with NULL resolved_at would make the whole AND NULL — three-
-- valued logic, not "false" — and NOT NULL is also NULL, so the row would be
-- SILENTLY EXCLUDED by the WHERE clause instead of correctly included. A row
-- with a real, non-estimated first_response_at but no resolution at all
-- (a plain on-time reply, no resolve yet) must still be frozen normally; the
-- explicit IS NOT NULL check makes that so.
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
  AND r.response_elapsed_at_met_seconds IS NULL
  AND NOT (r.resolved_at IS NOT NULL
           AND r.resolution_elapsed_at_met_seconds IS NULL
           AND r.first_response_at = r.resolved_at);

-- S6 (#235, #240, #243, #246): the response-side twin of S3. No estimated
-- gate is needed here either, for the same reason as S3: S5's own gate above
-- already leaves response_elapsed_at_met_seconds NULL for exactly the rows
-- whose first_response_at was S4's copy of an estimated resolution, and
-- `NULL > target` is never true. A record whose resolution is estimated but
-- whose first response is a real, earlier staff reply was never touched by
-- S5's gate (the equality check fails), so it keeps a real frozen number
-- here and this statement judges it normally — that reply's own breach
-- decision, exactly as #240 requires. A real reply landing on the exact same
-- microsecond as an updated_at-derived estimate would still be treated as
-- the estimate (S5's gate cannot tell them apart either), which errs in the
-- safe direction, as before.
UPDATE sla_records r
SET response_breached_at = r.first_response_at
FROM sla_policies p
WHERE p.id = r.policy_id
  AND r.first_response_at IS NOT NULL
  AND r.response_breached_at IS NULL
  AND r.response_elapsed_at_met_seconds > p.response_target_min::bigint * 60;

DROP TABLE m28_sla_resolution;
