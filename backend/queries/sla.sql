-- name: CreateSLAPolicy :exec
INSERT INTO sla_policies (id, name, priority, category_id, response_target_min, resolution_target_min)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetSLAPolicy :one
SELECT * FROM sla_policies WHERE id = $1;

-- name: UpdateSLAPolicy :exec
UPDATE sla_policies
SET name = $2, priority = $3, category_id = $4,
    response_target_min = $5, resolution_target_min = $6
WHERE id = $1;

-- name: DeleteSLAPolicy :exec
DELETE FROM sla_policies WHERE id = $1;

-- name: CountSLARecordsByPolicy :one
-- sla_records.policy_id is ON DELETE RESTRICT: a record's targets and breach
-- stamps only mean something against the policy that set them. Counting first
-- turns the raw foreign-key 500 into a refusal naming how many tickets depend
-- on the policy (#261) -- the same reason CountStatusHistoryByStatus exists.
SELECT COUNT(*) FROM sla_records WHERE policy_id = $1;

-- name: ListSLAPolicies :many
SELECT * FROM sla_policies ORDER BY priority, name;

-- name: FindSLAPolicy :one
-- The four tiers DESIGN.md documents, most specific first:
--   1. Priority + Category
--   2. Priority only      (category_id IS NULL = any category)
--   3. Category only      (priority IS NULL = any priority)
--   4. Catch-all          (neither set)
--
-- A NULL column means "matches anything", so the WHERE admits every candidate
-- and the ORDER BY picks the most specific. Before this, priority was NOT NULL
-- and the predicate required an exact match, so tiers 3 and 4 could neither be
-- stored nor matched.
SELECT * FROM sla_policies
WHERE (priority IS NULL OR priority = sqlc.arg(priority)::text)
  AND (category_id IS NULL OR category_id = sqlc.arg(category_id)::uuid)
ORDER BY
  CASE
    WHEN priority IS NOT NULL AND category_id IS NOT NULL THEN 1
    WHEN priority IS NOT NULL THEN 2
    WHEN category_id IS NOT NULL THEN 3
    ELSE 4
  END
LIMIT 1;

-- name: CreateSLARecord :exec
INSERT INTO sla_records (ticket_id, policy_id, first_response_at, resolved_at, response_breached_at, resolution_breached_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetSLARecord :one
SELECT * FROM sla_records WHERE ticket_id = $1;

-- name: UpdateSLARecord :exec
UPDATE sla_records
SET first_response_at = $2, resolved_at = $3,
    response_breached_at = $4, resolution_breached_at = $5
WHERE ticket_id = $1;

-- name: SetSLAFirstResponse :exec
-- Marks the first response, freezes elapsed-toward-target as of that same
-- moment, AND stamps a response breach if it is already late — all in ONE
-- statement. COALESCE-guarded like StampSLABreaches below: it only ever
-- writes first_response_at / response_elapsed_at_met_seconds /
-- response_breached_at, and only while each is still NULL, so it cannot race
-- with StampSLABreaches clobbering a breach stamp the way a full-row
-- UpdateSLARecord read-then-write could (see CLAUDE.md). Idempotent for the
-- same reason: a retried call finds every column already set and changes
-- nothing.
--
-- #228: this used to be the fact write alone, with the caller separately
-- reading the record, deciding whether to breach from that (possibly stale)
-- read, and issuing a second StampSLABreaches statement. Two consequences:
-- two near-simultaneous calls could both decide from a stale pre-write read,
-- so the LOSING call's own (later, larger) elapsed reading could still land
-- a breach stamp even though the WINNING call's earlier, on-time write is
-- what actually took — a stray, uncleared breach stamp next to a frozen
-- green elapsed reading. And a failure between the fact write and the
-- now-separate breach-stamp statement could lose a genuine late-response
-- signal permanently (ListSLABreachCandidates excludes a ticket once
-- first_response_at is set, so nothing ever revisits it).
--
-- Folding both into one statement removes the gap entirely: every column
-- reference on the right of "=" reads this row as it stood BEFORE this
-- statement (a single UPDATE's SET list is computed once, from the pre-image
-- row, never from another SET clause's new value), so
-- "first_response_at IS NULL" means "nothing has won this race yet — THIS
-- call's write is the one that lands, and its own elapsed reading is the one
-- the breach decision is made from." A losing call (first_response_at
-- already NOT NULL) touches nothing at all, including response_breached_at,
-- so it can never stamp a breach the winning call did not itself decide.
UPDATE sla_records
SET first_response_at = COALESCE(first_response_at, sqlc.arg(at)::timestamptz),
    response_elapsed_at_met_seconds = COALESCE(response_elapsed_at_met_seconds, sqlc.arg(elapsed_seconds)::bigint),
    response_breached_at = CASE
        WHEN first_response_at IS NULL
             AND sqlc.arg(elapsed_seconds)::bigint > sqlc.arg(response_target_seconds)::bigint
            THEN COALESCE(response_breached_at, sqlc.arg(at)::timestamptz)
        ELSE response_breached_at
    END
WHERE ticket_id = $1;

-- name: SetSLAResolved :exec
-- The resolution-side twin of SetSLAFirstResponse: see its comment for why
-- the fact write and the breach stamp are one statement (#228), and why
-- every right-hand-side column reference here reads the PRE-UPDATE row.
UPDATE sla_records
SET resolved_at = COALESCE(resolved_at, sqlc.arg(at)::timestamptz),
    resolution_elapsed_at_met_seconds = COALESCE(resolution_elapsed_at_met_seconds, sqlc.arg(elapsed_seconds)::bigint),
    resolution_breached_at = CASE
        WHEN resolved_at IS NULL
             AND sqlc.arg(elapsed_seconds)::bigint > sqlc.arg(resolution_target_seconds)::bigint
            THEN COALESCE(resolution_breached_at, sqlc.arg(at)::timestamptz)
        ELSE resolution_breached_at
    END
WHERE ticket_id = $1;

-- name: SetSLAResolvedAndFirstResponse :exec
-- #233: RecordResolved's own fold of TWO facts into one statement, the same
-- way #228 folded each fact with its own breach decision. Before this,
-- RecordResolved issued SetSLAResolved then SetSLAFirstResponse as two
-- separate statements (the #219 "a resolution is a response too" backfill).
-- A failure between them left resolved_at set and first_response_at
-- permanently NULL: RecordResolved's own fast no-op guard (resolved_at
-- already set) blocks any later call from ever retrying the second write,
-- and ListSLABreachCandidates' first_response_at IS NULL branch keeps
-- selecting the row on every sweep tick, which then stamps a PERMANENT false
-- response_breached_at. One statement makes that intermediate state
-- impossible: both writes commit together, in the same row version, or
-- neither does.
--
-- Same COALESCE-guarded, first-writer-wins shape as SetSLAFirstResponse /
-- SetSLAResolved, applied to both column pairs independently: each pair's
-- CASE reads only ITS OWN pre-image column (resolution_breached_at's
-- condition reads resolved_at; response_breached_at's reads
-- first_response_at), so a ticket that already has a genuine EARLIER
-- first_response_at (a real staff reply before the resolution) keeps it —
-- and its own already-decided response_breached_at — completely untouched;
-- only the resolution pair is written for it. This is exactly
-- RecordResolved's existing "SetFirstResponse's own COALESCE guard already
-- makes this the correct no-op" reasoning, now inside one statement instead
-- of two.
UPDATE sla_records
SET resolved_at = COALESCE(resolved_at, sqlc.arg(at)::timestamptz),
    resolution_elapsed_at_met_seconds = COALESCE(resolution_elapsed_at_met_seconds, sqlc.arg(elapsed_seconds)::bigint),
    resolution_breached_at = CASE
        WHEN resolved_at IS NULL
             AND sqlc.arg(elapsed_seconds)::bigint > sqlc.arg(resolution_target_seconds)::bigint
            THEN COALESCE(resolution_breached_at, sqlc.arg(at)::timestamptz)
        ELSE resolution_breached_at
    END,
    first_response_at = COALESCE(first_response_at, sqlc.arg(at)::timestamptz),
    response_elapsed_at_met_seconds = COALESCE(response_elapsed_at_met_seconds, sqlc.arg(elapsed_seconds)::bigint),
    response_breached_at = CASE
        WHEN first_response_at IS NULL
             AND sqlc.arg(elapsed_seconds)::bigint > sqlc.arg(response_target_seconds)::bigint
            THEN COALESCE(response_breached_at, sqlc.arg(at)::timestamptz)
        ELSE response_breached_at
    END
WHERE ticket_id = $1;

-- name: ListSLABreachCandidates :many
-- Tickets the breach sweep must evaluate: open, under a policy, with at least
-- one target that is neither met nor already stamped, using the same
-- pause-aware elapsed time as sla.Elapsed (see that function's doc comment;
-- the two must change together). LEAST(COALESCE(pending_since, now), now) is
-- the instant the SLA clock stopped: pending_since while the ticket is
-- currently Pending, clipped to now the same way Elapsed clips with
-- at.After(*PendingSince), and now otherwise. A Pending ticket whose frozen
-- elapsed time is still under target is therefore never selected. This
-- prefilter must still return everything EvaluateBreaches would stamp — it
-- stays a superset via <=, where Go's strict > decides the exact equality
-- instant on a fresh read of the row — see
-- TestSLAStore_ListBreachCandidates's superset invariant check.
SELECT r.ticket_id
FROM sla_records r
JOIN tickets      t ON t.id = r.ticket_id
JOIN sla_policies p ON p.id = r.policy_id
WHERE t.closed_at IS NULL
  AND (
       (r.first_response_at IS NULL AND r.response_breached_at IS NULL
          AND t.created_at
              + make_interval(secs => t.sla_paused_seconds::double precision)
              + make_interval(mins => p.response_target_min)
              <= LEAST(COALESCE(t.pending_since, sqlc.arg(now)::timestamptz), sqlc.arg(now)::timestamptz))
    OR (r.resolved_at IS NULL AND r.resolution_breached_at IS NULL
          AND t.created_at
              + make_interval(secs => t.sla_paused_seconds::double precision)
              + make_interval(mins => p.resolution_target_min)
              <= LEAST(COALESCE(t.pending_since, sqlc.arg(now)::timestamptz), sqlc.arg(now)::timestamptz))
  )
ORDER BY t.created_at;

-- name: ListSLARecordsByTicketIDs :many
-- Batch lookup for the per-ticket SLA status embedded on GET /tickets and
-- GET /tickets/{id} (#183): one query for the whole page, after it is
-- sliced, rather than a JOIN pushed into every one of the ~12 list/search
-- queries that would compute SLA for limit×(1+groups) rows and throw most
-- of them away. See sla.Service.StatusesFor.
SELECT * FROM sla_records WHERE ticket_id = ANY(sqlc.arg('ticket_ids')::uuid[]);

-- name: StampSLABreaches :exec
-- Sets only the breach columns, and only where still NULL. Two evaluators
-- racing on the same row cannot overwrite each other's stamp or, worse, the
-- request path's first_response_at / resolved_at. A stamp, once set, is a
-- fact about what happened (DESIGN.md) and is never cleared here.
UPDATE sla_records
SET response_breached_at   = COALESCE(response_breached_at,   sqlc.narg(response_breached_at)::timestamptz),
    resolution_breached_at = COALESCE(resolution_breached_at, sqlc.narg(resolution_breached_at)::timestamptz)
WHERE ticket_id = $1;
