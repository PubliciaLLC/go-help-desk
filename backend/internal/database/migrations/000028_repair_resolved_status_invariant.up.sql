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

-- Any ticket not currently in Resolved OR Closed has no business carrying a
-- resolved_at; NULL it so it no longer looks resolved to code that reads
-- resolved_at directly. Closed tickets are deliberately excluded:
-- applyStatusTimestamps's closedID case (ticket/service.go) only ever sets
-- ClosedAt and never touches ResolvedAt, by design, so a Closed ticket
-- keeping its old resolved_at is the CORRECT shape, not a violation. See
-- #208 — the original statement here (`s.name <> 'Resolved'`) also matched
-- every legitimately-Closed ticket and would have NULLed resolved_at across
-- the whole table.
UPDATE tickets t
SET resolved_at = NULL
FROM statuses s
WHERE t.status_id = s.id
  AND s.name NOT IN ('Resolved', 'Closed')
  AND t.resolved_at IS NOT NULL;

-- Any ticket sitting in Closed must carry a closed_at; stamp it with now()
-- for rows that were moved there without one.
UPDATE tickets t
SET closed_at = now()
FROM statuses s
WHERE t.status_id = s.id
  AND s.name = 'Closed'
  AND t.closed_at IS NULL;

-- A ticket sitting in Resolved must not carry a closed_at: pre-existing bug
-- described in resolveInTx's comment ("resolving a closed ticket clears
-- closed_at") means a ticket resolved-from-closed before that fix shipped
-- may still carry a stale closed_at alongside its (correct) resolved_at.
-- Clear it so a Resolved ticket's shape is unambiguous. See #208.
UPDATE tickets t
SET closed_at = NULL
FROM statuses s
WHERE t.status_id = s.id
  AND s.name = 'Resolved'
  AND t.closed_at IS NOT NULL;
