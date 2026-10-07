-- name: CreateUser :exec
INSERT INTO users (
    id,
    email,
    display_name,
    role,
    password_hash,
    mfa_secret,
    mfa_enabled,
    saml_subject,
    oidc_subject,
    created_at,
    updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 AND deleted_at IS NULL;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1 AND deleted_at IS NULL;

-- name: EmailIsTaken :one
-- Whether any row holds this address, INCLUDING a deleted one.
--
-- GetUserByEmail hides deleted rows, which is right for logging in and wrong
-- for this: the unique constraint is on every row, so a deleted account still
-- owns its address. Checking with the login query meant a signup for a
-- deleted account's address was accepted, a verification email went out, and
-- the link failed at the end with "invalid or already used token" — which is
-- exactly the dead end that check was added to prevent.
SELECT EXISTS (SELECT 1 FROM users WHERE email = $1);

-- name: GetUserBySAMLSubject :one
SELECT * FROM users WHERE saml_subject = $1 AND saml_subject != '' AND deleted_at IS NULL;

-- name: GetUserByOIDCSubject :one

SELECT * FROM users WHERE oidc_subject = $1 AND oidc_subject != '' AND deleted_at IS NULL;

-- name: UpdateUser :exec
UPDATE users
SET email = $2, display_name = $3, role = $4, password_hash = $5,
    mfa_secret = $6,
    mfa_enabled = $7,
    saml_subject = $8,
    oidc_subject = $9,
    updated_at = $10
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = now() WHERE id = $1;

-- name: DisableUser :exec
UPDATE users SET disabled = TRUE, updated_at = now() WHERE id = $1;

-- name: EnableUser :exec
UPDATE users SET disabled = FALSE, updated_at = now() WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users WHERE deleted_at IS NULL AND disabled = FALSE ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CountUsers :one
SELECT COUNT(*) FROM users WHERE deleted_at IS NULL AND disabled = FALSE;

-- name: SetUserPasswordHash :exec
-- Writes only the password hash.
--
-- SetPassword used to read the whole row, spend 45 to 66 milliseconds on
-- bcrypt, and then write every column back — so anything committed during
-- that window was undone. The window is controlled by the account holder,
-- which is what makes it serious: demote a compromised account, and a
-- password change already in flight writes `admin` back over the demotion.
-- The attacker then signs in, with their new password, as an administrator.
-- The same window undoes an administrator's MFA reset and reverts a
-- corrected email address.
--
-- AdminSetPassword has always been a narrow statement. This is the same thing
-- for the self-service path, which is the one an attacker can drive.
UPDATE users
SET password_hash = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: SetUserMFA :exec
-- Writes only the TOTP secret and whether it is enabled. Same reason: the
-- enrolment confirmation read the whole row, checked a code, and wrote every
-- column back over whatever had happened in between.
UPDATE users
SET mfa_secret = $2, mfa_enabled = $3, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: SyncFederatedUser :exec
-- What a federated login is allowed to change about an account it recognises:
-- the address, the name, and nothing else.
--
-- The known-identity paths of the SAML and OIDC upserts wrote the whole row
-- on every sign-in, carrying role, password hash and MFA state from a read a
-- few statements earlier. A login is something the account holder triggers at
-- will, so a demotion or a password reset landing in that window was written
-- back by the next sign-in.
UPDATE users
SET email = $2, display_name = $3, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateUserProfile :exec
-- The parts of a user an administrator edits: the address and the name.
--
-- Its own statement because UpdateUser writes the WHOLE row from a struct
-- read earlier in the request — role, password hash, MFA secret, federated
-- subjects — so anything that changed in between was silently written back.
-- Measured: read a user for a rename, have them change their password, let
-- the rename land, and the new password is refused while the old one works
-- again. The same shape undoes an MFA enrolment and another administrator's
-- role change.
--
-- A rename should rename. Everything else has its own path, and the role has
-- a guarded one.
UPDATE users
SET email = $2, display_name = $3, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListAssignableStaff :many
-- The people work can be given to: active staff and administrators, name and
-- id only.
--
-- Staff had no way to read a list of users at all — /admin/users is
-- administrator-only — so the assignee picker on the ticket page was empty
-- for every staff member, and no name could be resolved for anybody. The page
-- had to guess, and guessed wrong.
--
-- Everyone who is not deleted, with a flag for whether work can be given to
-- them. The flag rather than a filter, because the page needs both answers:
-- who can be picked, and whose name to show on a ticket that is already
-- assigned. Filtering to the assignable ones made a suspended colleague, or
-- one moved to a reporting role, render as "Former staff member" — which is a
-- statement about somebody having left, and it was not true. Re-enabling them
-- would have made the name reappear.
--
-- Deliberately narrow: an id, a display name and that flag. No email, no
-- role, no login state — that is the administrator's view.
SELECT id, display_name,
       (disabled = FALSE AND role IN ('staff', 'admin')) AS assignable
FROM users
WHERE deleted_at IS NULL
ORDER BY display_name;

-- name: DisableUserUnlessLastAdmin :one
-- Disables a user, refusing if that would leave the instance with no active
-- administrator. Returns the id when it applied, nothing when it did not.
--
-- Count and write in one statement, locking every active administrator row
-- first. Counting in Go and then writing was two statements with nothing
-- between them, and it lost: two parallel requests both counted before either
-- wrote. Measured, twenty-eight rounds in thirty ended with zero
-- administrators — and it did not need two people. One administrator sending
-- "remove Bob" and "remove me" together did it every time.
--
-- BEFORE ADDING PASSWORDLESS SIGN-IN, READ THIS.
--
-- This guard covers three ways to remove an administrator — disable, delete,
-- demote — and says nothing about removing their last way to AUTHENTICATE.
-- That is deliberate and currently correct, because a password is always a way
-- in: MFA enrolment sits outside RequireMFA in meRouter, so an administrator
-- with no working factor signs in with their password, reaches enrolment and
-- recovers without anyone's help. TestSoleAdministrator_CanSelfRecoverWith-
-- NoSecondFactor fails if that stops being true.
--
-- Passkeys as a password REPLACEMENT break it. With no password there is no
-- self-recovery, and an administrator whose last credential is removed is
-- locked out permanently — setup does not reopen. The fourth case belongs in
-- the change that introduces passwordless, in these statements, not in a
-- follow-up issue. See docs/DESIGN.md → Authentication → Passkeys, and the
-- entry in .claude/CLAUDE.md under Recorded architecture decisions.
--
-- The target carries `deleted_at IS NULL` of its own. The guard counted the
-- OTHER administrators as live ones, but the row it wrote was matched on id
-- alone — so an administrator holding a soft-deleted account's id could still
-- act on it, and SetUserRoleUnlessLastAdmin would happily mark a deleted row
-- `admin`. It could not tip the live-administrator count either way, since a
-- deleted row was never counted, and nothing restores such a row today. It is
-- closed anyway: a guarantee that holds only because no restore path happens
-- to exist is one that breaks the day somebody writes one.
--
-- deleted_at and not disabled: a suspended account is still an account, and
-- demoting, deleting or re-disabling one is ordinary administration. A
-- deleted account is gone.
--
-- FOR UPDATE over ALL of them, not just the others, because the lock sets
-- have to overlap: locking only the other administrators means two requests
-- lock different rows and neither waits. ORDER BY id so two of these cannot
-- deadlock. When the second one unblocks, Postgres re-checks the locked rows
-- against the WHERE, so a row the first request just demoted is no longer
-- counted.
WITH admins AS (
    SELECT u.id AS admin_id FROM users u
    WHERE u.role = 'admin' AND u.deleted_at IS NULL AND u.disabled = FALSE
    ORDER BY u.id
    FOR UPDATE
)
UPDATE users AS t
SET disabled = TRUE, updated_at = now()
WHERE t.id = $1
  AND t.deleted_at IS NULL
  AND (SELECT count(*) FROM admins WHERE admins.admin_id <> $1) > 0
RETURNING t.id;

-- name: SoftDeleteUserUnlessLastAdmin :one
-- The same guard for deletion. See DisableUserUnlessLastAdmin.
WITH admins AS (
    SELECT u.id AS admin_id FROM users u
    WHERE u.role = 'admin' AND u.deleted_at IS NULL AND u.disabled = FALSE
    ORDER BY u.id
    FOR UPDATE
)
UPDATE users AS t
SET deleted_at = now(), updated_at = now()
WHERE t.id = $1
  AND t.deleted_at IS NULL
  AND (SELECT count(*) FROM admins WHERE admins.admin_id <> $1) > 0
RETURNING t.id;

-- name: SetUserRoleUnlessLastAdmin :one
-- The same guard for a role change. See DisableUserUnlessLastAdmin.
--
-- A separate statement from UpdateUser because a role change is a different
-- operation from renaming somebody: it revokes sessions, it is refused to
-- machine credentials, and it is the one that can leave an instance with no
-- administrator.
WITH admins AS (
    SELECT u.id AS admin_id FROM users u
    WHERE u.role = 'admin' AND u.deleted_at IS NULL AND u.disabled = FALSE
    ORDER BY u.id
    FOR UPDATE
)
UPDATE users AS t
SET role = $2, updated_at = now()
WHERE t.id = $1
  AND t.deleted_at IS NULL
  AND ($2 = 'admin' OR (SELECT count(*) FROM admins WHERE admins.admin_id <> $1) > 0)
RETURNING t.id;

-- name: CountOtherActiveAdmins :one
-- How many administrators this instance would still have if $1 stopped being
-- one.
--
-- Nothing stopped an administrator disabling, demoting or deleting their own
-- sole admin account: all three answered 200 or 204, the next request was 401,
-- and setup does not reopen (HasUsers counts every row, deliberately). The
-- instance was then left with no way in at all short of editing the database.
SELECT COUNT(*) FROM users
WHERE role = 'admin'
  AND deleted_at IS NULL
  AND disabled = FALSE
  AND id <> $1;

-- name: ListActiveAdmins :many
-- Every active administrator, in full — not a count, because #300's guard has
-- to know WHICH of them still has a way to authenticate after an SSO
-- settings change, not just how many there are. See handler_admin_sso_guard.go.
SELECT * FROM users
WHERE role = 'admin'
  AND deleted_at IS NULL
  AND disabled = FALSE;

-- name: CountAllUsers :one
-- Every row, including disabled and soft-deleted accounts.
--
-- This is what gates /setup, and the filtered count above is why it had to
-- exist. Soft-delete or disable every account -- which an administrator can do
-- to their own, sole, admin account, since nothing stops them -- and the
-- filtered count returns zero, /setup/status answers {"needed": true}, and
-- anyone on the internet can POST /setup and be handed an administrator over
-- the existing data: every ticket, every customer, every attachment.
--
-- "Setup is permanently blocked once complete" is what the design says. A
-- count of live accounts cannot express "permanently"; a count of rows can,
-- because nothing in this system hard-deletes a user.
SELECT COUNT(*) FROM users;

-- name: GetUserByIDAdmin :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsersAdmin :many
SELECT * FROM users WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: RestoreUser :exec
UPDATE users SET deleted_at = NULL, updated_at = now() WHERE id = $1;

-- name: SetFirstUserMFA :execrows
-- First enrolment only: adopts the secret if, and only if, the account still
-- has no TOTP when the row is written (#338). The handler's "no factor yet"
-- check and the write used to be two statements, so concurrent first
-- confirms all passed the check, all wrote, and all answered success while
-- only the last held the account. Under READ COMMITTED a second UPDATE waits
-- on the first's row lock and re-evaluates this WHERE against the committed
-- row, so exactly one confirm matches. Zero rows means somebody else enrolled
-- first. Rotation of an existing secret uses SetUserMFA.
UPDATE users
SET mfa_secret = $2, mfa_enabled = true, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL AND NOT mfa_enabled;

-- name: ClearMFA :execrows
-- :execrows, not :exec: an UPDATE matching zero rows still reports no error,
-- so a nonexistent id looked like a successful clear. Service.ResetMFA writes
-- an audit entry once this returns without error, and #306's own adversarial
-- review caught that: a nonexistent target got a permanent, falsely-attributed
-- audit row for an account that never existed. The caller checks rows
-- affected and reports ErrNotFound when it is zero.
UPDATE users SET mfa_secret = '', mfa_enabled = false, updated_at = now() WHERE id = $1;

-- name: ClearFactors :one
-- Clears EVERY second factor an account holds and ends every session it has
-- open, as one statement. Returns how many users matched (0 or 1) and how many
-- passkeys went.
--
-- One statement so that it is all or nothing. reset-factors used to run three
-- (delete the passkeys, clear the TOTP columns, delete the sessions), and a
-- failure between the second and the third left an account with no factor and
-- its MFAPassed=true sessions alive -- the state the session revocation exists
-- to prevent (#307 item 4). A data-modifying CTE shares the statement's
-- snapshot and commits or rolls back with it, which a transaction would give
-- but without the plumbing; the ...UnlessLastAdmin statements are the same
-- shape for the same reason.
--
-- Passkeys as well as TOTP, because "Reset MFA" cleared only the TOTP columns:
-- on a passkey-only account the administrator got a success and the person
-- stayed locked out by the key they had lost (#307 item 2).
--
-- Sessions are ended here and not only by the caller for the reason the
-- reset-factors command spells out: a surviving session carries MFAPassed=true
-- from the factor just cleared, and could register a factor of its own.
--
-- Works on a disabled or deleted account, like ClearMFA before it.
WITH gone AS (
    DELETE FROM webauthn_credentials WHERE webauthn_credentials.user_id = $1 RETURNING webauthn_credentials.id
), ended AS (
    DELETE FROM sessions WHERE sessions.user_id = $1 RETURNING sessions.id
), cleared AS (
    UPDATE users
    SET mfa_secret = '', mfa_enabled = false, updated_at = now()
    WHERE id = $1
    RETURNING id
)
SELECT
    (SELECT count(*) FROM cleared)::int AS users_cleared,
    (SELECT count(*) FROM gone)::int    AS passkeys_removed;

-- name: AdminSetPassword :execrows
-- :execrows, for the same reason as ClearMFA above: a nonexistent id
-- reported success (204, no error) and still wrote an audit entry.
UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1;

-- name: ClaimMFAAttempt :one
-- Takes one attempt off the account's TOTP budget, and reports what is left.
--
-- Called BEFORE the code is checked, which is the whole point. The previous
-- order was read the lock, check the code, then count a failure -- three
-- statements, and every request that started before the first UPDATE landed
-- read "not locked" and went on to verify. Measured against the real server:
-- forty parallel wrong codes, thirty-six of them verified, limit five. An
-- attacker holding the password -- the exact case MFA exists for -- got a few
-- hundred guesses per window instead of five.
--
-- One UPDATE has no such window. Concurrent updates of one row serialise in
-- Postgres and each re-reads the row it is updating, so forty requests take
-- the numbers one to forty and the caller refuses everything past the budget.
--
-- Three cases, in the order the CASE tests them:
--
--   locked and still locked   the count keeps rising and the deadline does
--                             NOT move, so an attacker hammering a locked
--                             account cannot hold the owner out forever by
--                             pushing the lock further away.
--   locked and expired        the window is over: back to one, lock cleared.
--                             Without this the count stays at the maximum and
--                             the next single attempt re-locks immediately.
--   not locked                count it, and lock once the budget is spent.
UPDATE users
SET mfa_failed_attempts = CASE
        WHEN mfa_locked_until IS NOT NULL AND mfa_locked_until <= now() THEN 1
        ELSE mfa_failed_attempts + 1
    END,
    mfa_locked_until = CASE
        WHEN mfa_locked_until IS NOT NULL AND mfa_locked_until <= now() THEN NULL
        WHEN mfa_locked_until IS NOT NULL THEN mfa_locked_until
        WHEN mfa_failed_attempts + 1 >= sqlc.arg(max_attempts)::int
            THEN now() + make_interval(secs => sqlc.arg(lock_seconds)::int)
        ELSE mfa_locked_until
    END,
    updated_at = now()
WHERE id = $1
RETURNING mfa_failed_attempts, mfa_locked_until;

-- name: RecordMFAFailure :one
-- Counts a failed TOTP attempt and locks the account once the threshold is
-- reached. Returns the resulting lock time so the caller can refuse
-- immediately without a second round trip.
--
-- The count and the lock are set in one statement so concurrent attempts
-- cannot both read "4 failures" and both decide they are allowed.
UPDATE users
SET mfa_failed_attempts = mfa_failed_attempts + 1,
    mfa_locked_until = CASE
        WHEN mfa_failed_attempts + 1 >= sqlc.arg(max_attempts)::int
        THEN now() + make_interval(secs => sqlc.arg(lock_seconds)::int)
        ELSE mfa_locked_until
    END,
    updated_at = now()
WHERE id = $1
RETURNING mfa_failed_attempts, mfa_locked_until;

-- name: ClearMFAFailures :exec
-- Called after a correct code. NIST SP 800-63B has the verifier disregard
-- prior failed attempts once the user authenticates successfully.
UPDATE users
SET mfa_failed_attempts = 0, mfa_locked_until = NULL, updated_at = now()
WHERE id = $1;

-- name: GetMFALock :one
SELECT mfa_failed_attempts, mfa_locked_until FROM users WHERE id = $1;

-- name: AdoptUserByOIDCSubject :one
-- Binds an OIDC subject to a local account found by email address, and
-- reports whether it applied.
--
-- The conditions are the adoption rules, asked at the write rather than by
-- the caller: the account is live, it is not an administrator, it does not
-- federate via SAML, and it is not already bound to a different OIDC
-- subject. Adoption hands an account to whoever the identity provider says
-- owns that address, so every one of them matters.
--
-- One statement, because this used to be a read, a check in Go, and a write
-- of the whole row. That carried role, password hash and MFA state from the
-- read back over anything an administrator changed in between, and the checks
-- themselves were answered from the same stale copy: an account promoted
-- between the read and the write was adopted anyway, as an administrator.
--
-- The Go-side check still runs first, because it is what tells the person
-- WHICH rule refused them. This one is what binds.
UPDATE users
SET oidc_subject = sqlc.arg('oidc_subject'),
    display_name = COALESCE(sqlc.narg('display_name'), display_name),
    updated_at   = now()
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND disabled = FALSE
  AND role <> 'admin'
  AND saml_subject = ''
  AND oidc_subject IN ('', sqlc.arg('oidc_subject'))
RETURNING id;

-- name: EnableMFAIfStillEnrolled :one
-- Turns MFA on using whatever secret the row still holds, and reports whether
-- it applied.
--
-- The flag and nothing else. ConfirmMFAEnrollment used to read the row,
-- validate a code against the secret it found, and then write that same
-- secret back alongside the flag — a read-modify-write with a TOTP validation
-- in the middle of it. An administrator's "reset MFA" committing in that
-- window was undone: the cleared secret came back and MFA was re-enabled with
-- the authenticator the reset existed to revoke.
--
-- Writing only the flag removes the carried copy. The `mfa_secret <> ''` test
-- is what makes the reset win: once the secret is cleared there is nothing to
-- enable, no row comes back, and the caller reports that enrolment was
-- reset rather than silently turning MFA on against an empty secret.
UPDATE users
SET mfa_enabled = TRUE, updated_at = now()
WHERE id = $1 AND mfa_secret <> '' AND deleted_at IS NULL
RETURNING id;
