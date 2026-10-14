-- A ticket can have a history of SLA records (#443b).
--
-- sla_records had ticket_id as its primary key, so a ticket could have exactly
-- one record and its policy was immutable. Changing a ticket's priority
-- re-matches its SLA, and the rule is that a record's facts are never
-- rewritten: a re-match SUPERSEDES the current record and inserts a new one.
-- A ticket therefore has any number of rows, of which at most one is current.

ALTER TABLE sla_records ADD COLUMN id UUID NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE sla_records DROP CONSTRAINT sla_records_pkey;
ALTER TABLE sla_records ADD PRIMARY KEY (id);

-- NULL: the clock starts at the ticket's creation. That is every record before
-- this migration and every record AttachPolicy writes. Set only by a restart
-- or a re-match that carries a running clock over.
ALTER TABLE sla_records ADD COLUMN started_at TIMESTAMPTZ;

-- tickets.sla_paused_seconds, plus the part of an open Pending interval already
-- elapsed, as of started_at: pause time before the restart does not count
-- toward the new record.
ALTER TABLE sla_records ADD COLUMN paused_seconds_at_start BIGINT NOT NULL DEFAULT 0
    CHECK (paused_seconds_at_start >= 0);

-- Set once, when a re-match replaces this row. The ONLY column a re-match
-- writes on an existing row; every fact on it stays as it was.
ALTER TABLE sla_records ADD COLUMN superseded_at TIMESTAMPTZ;

-- At most one current record per ticket. Two concurrent re-matches cannot both
-- insert one.
CREATE UNIQUE INDEX sla_records_current_idx ON sla_records (ticket_id) WHERE superseded_at IS NULL;
CREATE INDEX sla_records_ticket_idx ON sla_records (ticket_id);
