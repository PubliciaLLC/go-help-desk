-- name: GetAttachmentReputation :one
-- The cached verdict for one hash from one provider.
--
-- Both halves of the key are required. Reading by hash alone would return
-- whichever provider happened to answer first and attribute it to the one the
-- operator has configured.
SELECT * FROM attachment_reputation
WHERE sha256 = $1 AND provider = $2;

-- name: UpsertAttachmentReputation :one
-- Records a completed lookup.
--
-- An upsert rather than an insert because two staff members can open the same
-- ticket at once: without ON CONFLICT the second lookup fails on the primary
-- key and an ordinary page render errors.
--
-- fetched_at is the database's clock, not Go's, for the same reason sessions
-- stopped passing a timestamp in: one clock decides one timeline.
--
-- clock_timestamp() rather than now(): #168 made this value the one two
-- refresh rules are decided on, and now() is the transaction's start time, not
-- the statement's. Two writes inside one transaction get the same stamp, and a
-- verdict written late in a long transaction is backdated to whenever that
-- transaction opened. Both errors point the same way — a row that looks older
-- than it is, re-checked sooner than it should be, out of an allowance that is
-- not ours.
--
-- Every write re-stamps it, including one that changes nothing, which is what
-- makes a re-check that comes back with the same answer still count as a
-- re-check.
INSERT INTO attachment_reputation (
    sha256, provider, state, detected, total, threat_name, analysed_at, fetched_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, clock_timestamp())
ON CONFLICT (sha256, provider) DO UPDATE
SET state       = EXCLUDED.state,
    detected    = EXCLUDED.detected,
    total       = EXCLUDED.total,
    threat_name = EXCLUDED.threat_name,
    analysed_at = EXCLUDED.analysed_at,
    fetched_at  = EXCLUDED.fetched_at
RETURNING *;
