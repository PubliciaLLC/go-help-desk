-- LOSES DATA: the superseded history is deleted, because ticket_id can be the
-- primary key again only when a ticket has a single row. The current record of
-- every ticket is kept, but it forgets started_at and paused_seconds_at_start,
-- so a record that a priority change restarted reads from the ticket's
-- creation again.
DELETE FROM sla_records WHERE superseded_at IS NOT NULL;

DROP INDEX sla_records_ticket_idx;
DROP INDEX sla_records_current_idx;

ALTER TABLE sla_records DROP CONSTRAINT sla_records_pkey;
ALTER TABLE sla_records ADD PRIMARY KEY (ticket_id);

ALTER TABLE sla_records DROP COLUMN superseded_at;
ALTER TABLE sla_records DROP COLUMN paused_seconds_at_start;
ALTER TABLE sla_records DROP COLUMN started_at;
ALTER TABLE sla_records DROP COLUMN id;
