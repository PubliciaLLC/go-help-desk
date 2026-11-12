-- No inverse. The up migration repairs data, it does not change the schema:
-- it cleared the assignee on tickets that were pointing at a deleted user, and
-- that is not a state worth restoring. Each ticket's audit entry
-- ("unassigned", no actor) names the deleted user it had, for anyone who wants
-- to look. Left as a no-op rather than guessing which entries to replay.
SELECT 1;
