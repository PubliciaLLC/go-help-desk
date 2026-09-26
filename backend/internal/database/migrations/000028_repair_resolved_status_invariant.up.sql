-- Repair legacy rows that predate the invariant AutoClose now assumes: a
-- ticket sitting in Resolved has resolved_at set and nothing else does; a
-- ticket sitting in Closed has closed_at set. See #191.
--
-- Before this, UpdateStatus (and any other second door into these statuses)
-- could move a ticket off Resolved without clearing resolved_at, or into
-- Closed without stamping closed_at. Combined with
-- ListResolvedTicketsBefore's now-added `status_id = $2` filter, a row that
-- still carries a stale resolved_at but sits in some other status would
-- simply stop being visible to the auto-close sweep at all — which is
-- correct going forward, but leaves the existing bad rows sitting there
-- looking like they were never resolved, or (for a Closed ticket with no
-- closed_at) not fully closed.

-- #230: this migration's destructive statements below match rows by STATUS
-- NAME ('Resolved', 'Closed'), but the admin API blocks deactivating a system
-- status without blocking a RENAME of one (handleUpdateStatus / SaveStatus).
-- If an admin renames one between deploy and this migration actually running
-- on an upgrade, the name-based exclusion below silently stops matching the
-- renamed status, reproducing #208's original data-loss bug through a
-- different path — irreversibly, since the down migration is a no-op.
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

-- Any ticket not currently in Resolved OR Closed has no business carrying a
-- resolved_at; NULL it so it no longer looks resolved to code that reads
-- resolved_at directly. Closed tickets are deliberately excluded:
-- applyStatusTimestamps's closedID case (ticket/service.go) only ever sets
-- ClosedAt and never touches ResolvedAt, by design, so a Closed ticket
-- keeping its old resolved_at is the CORRECT shape, not a violation. See
-- #208 — the original statement here (`s.name <> 'Resolved'`) also matched
-- every legitimately-Closed ticket and would have NULLed resolved_at across
-- the whole table.
--
-- #232: kind = 'system' added to this join too, alongside the guard above —
-- belt and suspenders, since the name column's UNIQUE constraint already
-- means a bare name join here can only ever match the row the (now
-- kind-checked) guard above just verified.
UPDATE tickets t
SET resolved_at = NULL
FROM statuses s
WHERE t.status_id = s.id
  AND s.kind = 'system'
  AND s.name NOT IN ('Resolved', 'Closed')
  AND t.resolved_at IS NOT NULL;

-- Any ticket sitting in Closed must carry a closed_at; stamp it with now()
-- for rows that were moved there without one. #232: kind = 'system', see above.
UPDATE tickets t
SET closed_at = now()
FROM statuses s
WHERE t.status_id = s.id
  AND s.kind = 'system'
  AND s.name = 'Closed'
  AND t.closed_at IS NULL;

-- A ticket sitting in Resolved must not carry a closed_at: pre-existing bug
-- described in resolveInTx's comment ("resolving a closed ticket clears
-- closed_at") means a ticket resolved-from-closed before that fix shipped
-- may still carry a stale closed_at alongside its (correct) resolved_at.
-- Clear it so a Resolved ticket's shape is unambiguous. See #208. #232:
-- kind = 'system', see above.
UPDATE tickets t
SET closed_at = NULL
FROM statuses s
WHERE t.status_id = s.id
  AND s.kind = 'system'
  AND s.name = 'Resolved'
  AND t.closed_at IS NOT NULL;

-- #231: the #226(a)/(b) SLA backfill (moved here from migration
-- 000027_sla_frozen_elapsed.up.sql — see that file's own note) for
-- pre-existing sla_records rows that violate the OLD rules #219/#220 fixed
-- in code but not in already-written data. It has to run HERE, after the two
-- repairs directly above: it used to run inside 000027, BEFORE this file,
-- keyed on `t.closed_at IS NOT NULL` — but a Closed ticket that had never had
-- closed_at stamped (exactly the row the "must carry a closed_at" repair
-- above exists to fix) still had a NULL closed_at at that earlier point, so
-- 000027's old backfill skipped precisely the rows this file's own repair
-- was about to fix. These legacy rows landed in the permanent
-- live-growing-red-indicator state #226(b)/#220 were filed to eliminate,
-- with nothing in the running code ever touching them afterward
-- (RecordResolved's no-op guard and ListSLABreachCandidates' NULL-timestamp
-- selection both assume this backfill already ran once).
--
-- Keyed on the ticket's CURRENT status (name = 'Closed', kind = 'system' —
-- same #232 reasoning as the repairs above), never on closed_at at all: a
-- ticket carrying a STALE closed_at from the pre-fix UpdateStatus
-- Closed→open path — which the repair above does not clear, only Resolved
-- tickets have closed_at cleared — would otherwise still match
-- `closed_at IS NOT NULL` while it sits open and is actively being worked,
-- freezing a false "resolved" reading onto a ticket that has not resolved.
-- Restricted to 'Closed' (not 'Resolved' too): a ticket currently Resolved
-- already has its own resolved_at stamped by resolveInTx on every path into
-- that status, so backfilling it here is never this statement's job.
UPDATE sla_records r
SET resolved_at = COALESCE(t.resolved_at, t.closed_at)
FROM tickets t
JOIN statuses s ON s.id = t.status_id
WHERE t.id = r.ticket_id
  AND r.resolved_at IS NULL
  AND s.kind = 'system'
  AND s.name = 'Closed';

-- Freeze resolution_elapsed_at_met_seconds for exactly the rows the backfill
-- above just gave a resolved_at to (its own WHERE excludes every row already
-- carrying one, including whatever migration 000027's own earlier pass
-- already froze) — same formula as that pass, now with a value to compute it
-- from.
--
-- #235: also stamps resolution_breached_at when the backfilled instant was
-- already past the policy's resolution target, using the same threshold
-- SetSLAResolved computes at record time (#217/#228). Migration 000027's
-- original comment claimed this backfill wrote "exactly what RecordResolved
-- would have written" — true for the timestamp and frozen-elapsed columns,
-- but RecordResolved (post-#217/#228) also stamps a breach, and the old
-- backfill never did. Left unstamped, a legacy ticket that really was late
-- gets a correct (red) frozen elapsed reading but a permanently NULL breach
-- column — and nothing can ever set it afterward: r.resolved_at is no longer
-- NULL once this runs, so ListSLABreachCandidates never selects the row
-- again.
-- Postgres does not allow the UPDATE target's own alias (r) inside a FROM
-- clause's JOIN ... ON — only in the WHERE clause — so sla_policies is
-- brought in as a second, comma-joined FROM item (p.id = r.policy_id moves to
-- WHERE) rather than an explicit JOIN against tickets.
UPDATE sla_records r
SET resolution_elapsed_at_met_seconds = GREATEST(
        0,
        EXTRACT(EPOCH FROM (r.resolved_at - t.created_at))::bigint - t.sla_paused_seconds
    ),
    resolution_breached_at = CASE
        WHEN GREATEST(
                 0,
                 EXTRACT(EPOCH FROM (r.resolved_at - t.created_at))::bigint - t.sla_paused_seconds
             ) > p.resolution_target_min * 60
            THEN COALESCE(r.resolution_breached_at, r.resolved_at)
        ELSE r.resolution_breached_at
    END
FROM tickets t, sla_policies p
WHERE t.id = r.ticket_id
  AND p.id = r.policy_id
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
-- rule, whichever door originally supplied that resolved_at. Unlike the
-- resolved_at backfill above, this has no status-name key at all — it never
-- did, and needs none: it depends only on sla_records' own columns
-- (resolved_at IS NOT NULL, first_response_at IS NULL), which by this point
-- already reflect the correct, fully-repaired resolved_at, whichever door
-- supplied it.
UPDATE sla_records r
SET first_response_at = r.resolved_at
FROM tickets t
WHERE t.id = r.ticket_id
  AND r.resolved_at IS NOT NULL
  AND r.first_response_at IS NULL;

-- Freeze response_elapsed_at_met_seconds for exactly the rows the backfill
-- above just gave a first_response_at to, using the same pending-aware clip
-- as migration 000027's own response_elapsed_at_met_seconds pass (see #222)
-- — first response and resolution landed at the same instant for these rows,
-- so the same formula applies.
--
-- #235: also stamps response_breached_at when that instant was already past
-- the policy's response target — same reasoning as the resolution-side pass
-- above.
UPDATE sla_records r
SET response_elapsed_at_met_seconds = GREATEST(
        0,
        EXTRACT(EPOCH FROM (r.first_response_at - t.created_at))::bigint - t.sla_paused_seconds
            - CASE
                  WHEN t.pending_since IS NOT NULL
                  THEN GREATEST(0, EXTRACT(EPOCH FROM (r.first_response_at - t.pending_since)))::bigint
                  ELSE 0
              END
    ),
    response_breached_at = CASE
        WHEN GREATEST(
                 0,
                 EXTRACT(EPOCH FROM (r.first_response_at - t.created_at))::bigint - t.sla_paused_seconds
                     - CASE
                           WHEN t.pending_since IS NOT NULL
                           THEN GREATEST(0, EXTRACT(EPOCH FROM (r.first_response_at - t.pending_since)))::bigint
                           ELSE 0
                       END
             ) > p.response_target_min * 60
            THEN COALESCE(r.response_breached_at, r.first_response_at)
        ELSE r.response_breached_at
    END
FROM tickets t, sla_policies p
WHERE t.id = r.ticket_id
  AND p.id = r.policy_id
  AND r.first_response_at IS NOT NULL
  AND r.response_elapsed_at_met_seconds IS NULL;
