-- name: CreateWebAuthnCredential :exec
-- Records a credential after its registration ceremony has been verified.
--
-- Nothing calls this until the attestation has been checked: the design's rule
-- is that a staged challenge never reaches the account until the person has
-- proved they hold the key, which is why TOTP grew GenerateMFASecret and
-- ConfirmMFAEnrollmentWith in place of the older EnrollMFA.
INSERT INTO webauthn_credentials (
    user_id, credential_id, public_key, sign_count, transports,
    aaguid, backup_eligible, backup_state, name
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ListWebAuthnCredentialsByUser :many
-- Everything this person has registered, newest last so the list reads in the
-- order they added them.
SELECT * FROM webauthn_credentials
WHERE user_id = $1
ORDER BY created_at;

-- name: GetWebAuthnCredentialByCredentialID :one
-- The assertion arrives carrying a credential id and nothing else, so this is
-- the lookup sign-in depends on. The unique index on credential_id serves it.
SELECT * FROM webauthn_credentials WHERE credential_id = $1;

-- name: CountWebAuthnCredentialsForUser :one
-- Whether this account has a passkey at all.
--
-- This is what makes MFARequiredFor satisfiable by a passkey rather than only
-- by TOTP, and it is a count rather than an EXISTS because the same question
-- is asked when deciding whether removing one leaves the account with none.
SELECT count(*) FROM webauthn_credentials WHERE user_id = $1;

-- name: TouchWebAuthnCredential :exec
-- Records a successful assertion: the counter the authenticator reported, and
-- when it was last used.
--
-- Written off the authentication path, so a sign-in never waits on it and
-- never fails because of it. That also means two sign-ins can land out of
-- order, so the counter only ever goes up: a late write of a lower value would
-- put the stored counter back below one a cloned key has already used, and the
-- clone check (#616b) compares against what is stored here.
UPDATE webauthn_credentials
SET sign_count = GREATEST(sign_count, $2), last_used_at = now()
WHERE id = $1;

-- name: DeleteWebAuthnCredential :one
-- Removes one credential, and reports whether it was this user's to remove.
--
-- Scoped by user_id as well as id, in the statement rather than by a check in
-- Go: an id is not an authorisation, and the caller having read the row a
-- moment ago is the read-then-write shape the rest of this codebase has spent
-- several rounds removing.
DELETE FROM webauthn_credentials
WHERE id = $1 AND user_id = $2
RETURNING id;

-- name: RemoveUserPasskey :one
-- An administrator removes ONE credential of another user, and every session
-- that user has goes with it, as one statement (#615, D1: always, not only when
-- it was the last factor; the key is presumed lost or compromised).
--
-- The credential is matched on its owner as well as its id, in the statement:
-- an id is not an authorisation, and a credential that is not this user's is
-- reported as nothing removed. Sessions end only when a credential went, so
-- naming somebody else's key under this user's path cannot be used to sign the
-- user out.
--
-- One statement, like ClearFactors, so that a failure between the two cannot
-- leave the key gone and its sessions alive, or the other way round. The
-- sessions matter because one may carry MFAPassed from the key just removed.
WITH gone AS (
    DELETE FROM webauthn_credentials
    WHERE webauthn_credentials.id = $1 AND webauthn_credentials.user_id = $2
    RETURNING webauthn_credentials.id
), ended AS (
    DELETE FROM sessions
    WHERE sessions.user_id = $2 AND EXISTS (SELECT 1 FROM gone)
    RETURNING sessions.id
)
SELECT
    (SELECT count(*) FROM gone)::int  AS passkeys_removed,
    (SELECT count(*) FROM ended)::int AS sessions_ended;
