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

-- Any ticket not currently in Resolved has no business carrying a
-- resolved_at; NULL it so it no longer looks resolved to code that reads
-- resolved_at directly.
UPDATE tickets t
SET resolved_at = NULL
FROM statuses s
WHERE t.status_id = s.id
  AND s.name <> 'Resolved'
  AND t.resolved_at IS NOT NULL;

-- Any ticket sitting in Closed must carry a closed_at; stamp it with now()
-- for rows that were moved there without one.
UPDATE tickets t
SET closed_at = now()
FROM statuses s
WHERE t.status_id = s.id
  AND s.name = 'Closed'
  AND t.closed_at IS NULL;
