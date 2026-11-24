-- Catch-all and category-only policies cannot survive the column becoming NOT
-- NULL again, so they are removed rather than silently rewritten to a priority
-- they were never intended to have.
--
-- sla_records.policy_id is ON DELETE RESTRICT, so this rollback fails outright
-- if any ticket is still tracking against such a policy. That is the intended
-- outcome: losing a ticket's SLA record is worse than a refused rollback.
DELETE FROM sla_policies WHERE priority IS NULL;
ALTER TABLE sla_policies ALTER COLUMN priority SET NOT NULL;
