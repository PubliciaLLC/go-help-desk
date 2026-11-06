-- Indexes for the admin-wide audit view (#129) and its retention purge.
--
-- audit_log_entity_idx (entity_type, entity_id), from 000001, already serves
-- the per-ticket feed. Two new query shapes need their own indexes:
--   - the admin-wide search, filtered by action and/or actor and ordered by
--     created_at, across every entity rather than one
--   - the retention purge, deleting everything before a cutoff
-- Both would otherwise scan the whole table.

CREATE INDEX audit_log_created_at_idx ON audit_log (created_at);
CREATE INDEX audit_log_actor_id_idx ON audit_log (actor_id) WHERE actor_id IS NOT NULL;
CREATE INDEX audit_log_action_idx ON audit_log (action);
