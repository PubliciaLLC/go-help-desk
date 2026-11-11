-- name: ReplyMatchesForTickets :many
-- One snippet per ticket: its newest matching reply the viewer may read. The
-- second half of reply search; the first is the reply_hits CTE in
-- ListTicketsFiltered, and both take the same include_internal.
--
-- Run only for the tickets on the page (at most 200). ts_headline parses the
-- body again, so it runs once per ticket, not once per matching reply.
--
-- include_internal comes from the actor's role and nothing else (see
-- ticket.IncludeInternalFor). It is applied HERE as well as in the match
-- query, because a ticket that matches on its own subject must not show a
-- snippet from an internal note, to a requester least of all.
--
-- The expression must be spelled to_tsvector('english', r.body) exactly, or the
-- planner will not use ticket_replies_body_fts_idx (migration 000061).
--
-- Matched words are wrapped in U+0002 and U+0003, not HTML. No author is read.
SELECT m.ticket_id,
       m.id AS reply_id,
       m.internal,
       m.created_at,
       ts_headline('english', m.body, to_tsquery('english', sqlc.arg(search_query)::text),
                   'MaxFragments=1, MaxWords=24, MinWords=10, StartSel=' || chr(2) || ', StopSel=' || chr(3))::text AS snippet
FROM unnest(sqlc.arg(ticket_ids)::uuid[]) AS tid(id)
CROSS JOIN LATERAL (
  SELECT r.ticket_id, r.id, r.internal, r.created_at, r.body
  FROM ticket_replies r
  WHERE r.ticket_id = tid.id
    AND to_tsvector('english', r.body) @@ to_tsquery('english', sqlc.arg(search_query)::text)
    AND (sqlc.arg(include_internal)::bool OR NOT r.internal)
  ORDER BY r.created_at DESC, r.id
  LIMIT 1
) m;

-- name: ReplySearchReady :one
-- Whether reply search may run: both indexes it needs exist and are valid.
-- The server builds them in the background after the migration, and only once
-- every existing reply has its body_tsv (internal/database/replyindex), so a
-- valid index means the column is complete: a reply written since the
-- migration is filled by the trigger, an older one by the background fill.
-- Until then a search skips replies and says so.
--
-- indisvalid is false for the index a failed CREATE INDEX CONCURRENTLY leaves
-- behind, which still exists under its name and must not count.
SELECT (
  EXISTS (SELECT 1 FROM pg_catalog.pg_index WHERE indexrelid = to_regclass('ticket_replies_body_tsv_idx') AND indisvalid AND indisready)
  AND EXISTS (SELECT 1 FROM pg_catalog.pg_index WHERE indexrelid = to_regclass('ticket_replies_created_at_idx') AND indisvalid AND indisready)
)::bool AS ready;
