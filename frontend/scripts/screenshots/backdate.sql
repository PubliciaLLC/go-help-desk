-- Screenshot tooling only. Never part of the product, never run by the server.
--
-- The API stamps every ticket with the time it was filed, so a freshly seeded
-- instance has no history: the dashboard's 30-day trend is one spike on today,
-- Reports has nothing to compare with, and no SLA target is anywhere near its
-- limit. seed.mjs --extras files the history anyway and writes where each ticket
-- should sit in time to history.json. This moves each ticket there, together
-- with its replies, its status changes and its SLA record, so the rows agree
-- with each other.
--
-- How it is run (scripts/screenshots.sh, between the extras seed and the extras
-- capture), by hand it would be:
--
--   psql -d ghd_screenshots -v ON_ERROR_STOP=1 -v spec="$(cat history.json)" -f backdate.sql
--
-- Two guards: it stops unless the database is named ghd_screenshots*, and it
-- stops unless every ticket named in the spec exists. It is deterministic: every
-- instant is now() (fixed once, for the whole transaction) minus a fixed number
-- of minutes taken from the spec. There is no randomness. It touches only the
-- tickets in the spec; the audit log keeps the real time of each action, and no
-- page that is captured after this step shows it.

DO $$
BEGIN
  IF current_database() !~ '^ghd_screenshots[A-Za-z0-9_]*$' THEN
    RAISE EXCEPTION 'backdate.sql runs only against a database named ghd_screenshots*, not %', current_database();
  END IF;
END $$;

BEGIN;

-- id, and in minutes: c age, fr first staff reply after filing, res resolution
-- after filing, clo close after the resolution, upd time since the last change,
-- ro time since the ticket was reopened. A key the ticket has no use for is absent.
CREATE TEMP TABLE spec ON COMMIT DROP AS
SELECT (e->>'id')::uuid AS id,
       (e->>'c')::int   AS c,
       (e->>'fr')::int  AS fr,
       (e->>'res')::int AS res,
       (e->>'clo')::int AS clo,
       (e->>'upd')::int AS upd,
       (e->>'ro')::int  AS ro
FROM jsonb_array_elements(:'spec'::jsonb) AS e;

DO $$
DECLARE
  missing int;
  incomplete int;
BEGIN
  SELECT count(*) INTO missing FROM spec s WHERE NOT EXISTS (SELECT 1 FROM tickets t WHERE t.id = s.id);
  IF missing > 0 THEN
    RAISE EXCEPTION 'backdate.sql: % ticket(s) in the spec do not exist', missing;
  END IF;
  -- A ticket that was resolved, closed or reopened needs the time it was.
  SELECT count(*) INTO incomplete FROM spec s JOIN tickets t ON t.id = s.id
  WHERE (t.resolved_at IS NOT NULL AND s.res IS NULL)
     OR (t.closed_at IS NOT NULL AND (s.res IS NULL OR s.clo IS NULL))
     OR (t.last_reopened_at IS NOT NULL AND s.ro IS NULL);
  IF incomplete > 0 THEN
    RAISE EXCEPTION 'backdate.sql: % ticket(s) were resolved, closed or reopened but the spec gives no time for it', incomplete;
  END IF;
END $$;

-- shift.d is how far each ticket moves: from the instant it was filed to the instant it should have been.
CREATE TEMP TABLE shift ON COMMIT DROP AS
SELECT s.id,
       now() - s.c * interval '1 minute' AS created,
       (now() - s.c * interval '1 minute') - t.created_at AS d
FROM spec s JOIN tickets t ON t.id = s.id;

-- Replies and status changes keep their order and their spacing; they move with the ticket.
UPDATE ticket_replies r SET created_at = r.created_at + sh.d
FROM shift sh WHERE r.ticket_id = sh.id;

UPDATE ticket_status_history h SET created_at = h.created_at + sh.d
FROM shift sh WHERE h.ticket_id = sh.id;

UPDATE tickets t SET
  created_at       = sh.created,
  resolved_at      = CASE WHEN t.resolved_at IS NOT NULL THEN sh.created + s.res * interval '1 minute' END,
  closed_at        = CASE WHEN t.closed_at IS NOT NULL THEN sh.created + (s.res + s.clo) * interval '1 minute' END,
  -- A ticket waiting on someone has the SLA clock stopped at the moment it started waiting.
  pending_since    = CASE WHEN t.pending_since IS NOT NULL THEN t.pending_since + sh.d END,
  last_reopened_at = CASE WHEN t.last_reopened_at IS NOT NULL THEN now() - s.ro * interval '1 minute' END,
  updated_at       = now() - s.upd * interval '1 minute'
FROM spec s JOIN shift sh ON sh.id = s.id
WHERE t.id = s.id;

-- The current SLA record of each ticket that has one: the times of the first
-- reply and of the resolution, what the clock read at those moments, and the
-- breach stamps the sweep would have written when a target was missed.
UPDATE sla_records r SET
  first_response_at               = CASE WHEN s.fr IS NOT NULL THEN sh.created + s.fr * interval '1 minute' END,
  response_elapsed_at_met_seconds = CASE WHEN s.fr IS NOT NULL THEN s.fr * 60 END,
  response_breached_at            = CASE WHEN s.fr > p.response_target_min
                                         THEN sh.created + p.response_target_min * interval '1 minute' END,
  resolved_at                     = CASE WHEN s.res IS NOT NULL THEN sh.created + s.res * interval '1 minute' END,
  resolution_elapsed_at_met_seconds = CASE WHEN s.res IS NOT NULL THEN s.res * 60 END,
  resolution_breached_at          = CASE WHEN s.res > p.resolution_target_min
                                         THEN sh.created + p.resolution_target_min * interval '1 minute' END,
  response_at_risk_at             = NULL,
  resolution_at_risk_at           = NULL
FROM spec s
JOIN shift sh ON sh.id = s.id
JOIN sla_policies p ON TRUE
WHERE r.ticket_id = s.id AND r.superseded_at IS NULL AND p.id = r.policy_id;

SELECT count(*) AS tickets_moved,
       (SELECT count(*) FROM sla_records r JOIN spec s ON s.id = r.ticket_id WHERE r.superseded_at IS NULL) AS sla_records_moved
FROM spec;

COMMIT;
