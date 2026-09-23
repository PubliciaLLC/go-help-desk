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
-- stopped passing a timestamp in: one clock decides one timeline. now() rather
-- than clock_timestamp() is fine here — nothing compares this value against an
-- expiry, it is only shown to a person.
INSERT INTO attachment_reputation (
    sha256, provider, state, detected, total, threat_name, analysed_at, fetched_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (sha256, provider) DO UPDATE
SET state       = EXCLUDED.state,
    detected    = EXCLUDED.detected,
    total       = EXCLUDED.total,
    threat_name = EXCLUDED.threat_name,
    analysed_at = EXCLUDED.analysed_at,
    fetched_at  = EXCLUDED.fetched_at
RETURNING *;
