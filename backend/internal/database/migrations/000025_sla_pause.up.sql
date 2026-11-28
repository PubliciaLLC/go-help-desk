-- SLA timers pause while a ticket is Pending (DESIGN.md, "Timer Mechanics").
--
-- On tickets rather than sla_records: the boundary of a paused interval has
-- to commit in the same transaction as the status change that opened or
-- closed it, and the ticket row is the one thing every status door already
-- locks and rewrites. A pause boundary committed apart from its status change
-- is a corrupted clock.
--
-- pending_since is the start of the CURRENTLY open Pending interval, NULL
-- when the ticket is not Pending. sla_paused_seconds is the sum of every
-- CLOSED interval. Elapsed-toward-target is computed from both in Go
-- (sla.Elapsed); nothing here is derived on read.
ALTER TABLE tickets
    ADD COLUMN pending_since      TIMESTAMPTZ,
    ADD COLUMN sla_paused_seconds BIGINT NOT NULL DEFAULT 0
        CHECK (sla_paused_seconds >= 0);

-- A ticket sitting in Pending at upgrade time must not look like it is
-- making progress toward breach from now on. Its open interval starts at
-- its most recent status-history row. Intervals that closed before this
-- migration are NOT reconstructed: no scheduler has ever stamped a breach in
-- production (#182), so there is nothing to correct retroactively.
UPDATE tickets t
SET    pending_since = h.created_at
FROM   (SELECT DISTINCT ON (ticket_id) ticket_id, created_at
        FROM   ticket_status_history
        ORDER  BY ticket_id, created_at DESC) h,
       statuses s
WHERE  h.ticket_id = t.id
  AND  s.id = t.status_id
  AND  s.name = 'Pending';
