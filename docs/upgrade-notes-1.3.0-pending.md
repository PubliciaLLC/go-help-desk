# Upgrade Notes for v1.3.0 (Pending)

These notes document configuration and environment changes in v1.3.0. They will be folded into the release notes by PR #438.

## Antivirus Scanning: Banner for Offline Policies

Administrators see a banner on the admin panel with the exact fix when any attachment exists, no scan policy was ever saved, and no scanner address is set:

1. Add `COMPOSE_PROFILES=antivirus` and `CLAMAV_ADDR=tcp://clamav:3310` to `docker/.env`
2. Run `docker compose up -d`
3. Confirm the scanner address in Settings → Attachments

The banner reads:

> Attachments are not being scanned. To turn scanning back on, add `COMPOSE_PROFILES=antivirus` and `CLAMAV_ADDR=tcp://clamav:3310` to `docker/.env`, run `docker compose up -d`, then confirm the scanner address under Settings → Attachments. To keep scanning off, choose "Do not scan" there.

The scanner address counts as set if it is in Settings or in the environment. Settings → Attachments also shows the scanner's policy, whether an address is configured, whether it answers, and what that means for uploads.

Explicitly choosing "Do not scan" in the policy dropdown clears the banner. The exception is documented in DESIGN.md:1177-1183 — until a policy is explicitly set, the banner warns about unscanned uploads even in "off" mode.

## New Environment Variables (Compose-Forwarded)

The following variables are now honoured when set in `docker/.env`. They are automatically forwarded to the app container and have sensible defaults in Docker Compose:

### `AUTH_RATE_LIMIT_PER_MINUTE`

- **Default**: `10`
- **Description**: Failed password attempts per account per minute before a 429 response. `0` disables both password-attempt limiting and signup limiting. In-process counters reset on restart; each replica maintains its own budget, so N replicas multiply the limit by N.
- **Note**: TOTP verification has its own strict budget (durable on the user row) and is not affected by this setting.

### `AUTH_THROTTLE_DELAY`

- **Default**: `1s`
- **Description**: How long a login waits before checking the password, for accounts that have spent their failed-attempt budget. Bounds online guessing to the server's bcrypt throughput (roughly 10–15 guesses/second/core). Must be a positive duration (e.g., `1s`, `500ms`, `1m`); cannot be zero at startup.
- **Impact**: A legitimate user who exhausts the budget still succeeds — their correct password is always honoured, just after the delay.

## JWT Secret (`JWT_SECRET`)

`JWT_SECRET` is now **optional**:

- If **unset or under 32 bytes**: A random 32-byte key is generated once and stored in the database. All instances in the cluster read the same key from the database.
- If **32+ bytes**: The environment value is used as-is (the long value wins over the generated key).
- If it is **exactly one of the example secrets published in this repository** (for example `change-me-in-production-this-is-really-insecure`): it is discarded, a generated key is used, and the server logs one warning. Anyone can read the example, so it must never sign anything. (`SESSION_SECRET` is different: an example value there is still flagged in the admin panel, not discarded.)
- **Upgrade path**: If you set `JWT_SECRET` to a long value in `.env`, it remains in use and nothing changes. If you unset it or provide a short value, a new key is generated on first boot. **OAuth clients must fetch a new access token once** (sessions are unaffected; they use `SESSION_SECRET`).
- **Backup implications**: Database backups now contain the JWT signing key. Restoring a backup onto another instance carries the key with it (tokens remain valid there).
- **Rotation**: To rotate the key, delete the row from `instance_secrets` and restart. All OAuth tokens issued with the old key become invalid.

## `APP_ENV` Variable (Removed from Documentation)

The `APP_ENV` environment variable no longer has any effect. Setting it is harmless and will not break anything, but it is no longer documented. If you set it in your `.env`, you can safely ignore it or remove it; the server logs a single info message at startup whenever it is set, whatever its value. The log level is controlled by `LOG_LEVEL`.

## Attachment Storage Check (`ATTACHMENT_DIR`)

At startup the server checks that `ATTACHMENT_DIR` (default `/data/attachments`, fixed to the `attachments` volume under Compose) exists and can be written to. If it cannot:

- the server logs a warning and administrators see a banner, "Attachment uploads are disabled", with the reason;
- every upload (staff, requester and guest) is refused with `503 attachments_unavailable` until the directory is fixed. Everything else runs, and attachments already stored can still be downloaded while the directory is readable;
- the check repeats on each upload, so fixing the permissions needs no restart.

Before this, an unwritable directory showed up as a failed upload partway through.

## Admin API: data left behind, half-applied edits and path checks (#419)

Scripts and API keys that call the admin API can see these changes. Nothing needs doing in the admin UI.

- **Deleting a category, type or item also deletes its custom-field assignments** (a category's include those of its types and items). They used to be left behind, pointing at a record that no longer existed. Assignments left over from deletes made on earlier versions are removed once by migration 000039 (those whose category, type or item no longer exists); it cannot be undone, and the rows it removes pointed at nothing.
- **Deleting a group now writes an `unassigned` audit entry for every ticket it held**, open or not, in the same transaction as the delete. Before, the tickets silently lost their group with no entry. The entry's before and after values name `assignee_group_id`, which the audit view now shows.
- **`PATCH /admin/users/{id}` checks every field before it writes any.** `{"disabled": true, "email": "bad"}` answers 400 and leaves the account enabled; it used to disable the account, end its sessions and then answer 400. The same goes for an email another account holds (409) and a change that would remove the last active administrator (400): both are refused before anything is written, where `{"reset_mfa": true, "role": "user"}` on the only administrator used to clear their factors and then answer 400.
- **Soft-deleted accounts answer 404** to `PATCH /admin/users/{id}` (for any body, `{}` included, which used to answer 200 with the deleted record) and to `POST /admin/users/{id}/password` (which used to set the deleted account's password and answer 204).
- **Same order of checks for a session and an API key** on the password reset and on PATCH: the body is checked first (400), then the caller (403), then the account (404). Before, an API key aimed at an administrator or a deleted account got 403 or 404 before its body was looked at.
- **Mismatched ids in a URL answer 404.** `/categories/{id}/types/{typeId}` (PATCH, DELETE), the item routes under it, and all nine custom-field assignment routes now require every id to exist and to belong to the one before it, and an assignment to belong to the scope in the path. Before, only the deepest id was read, so a wrong parent id was ignored. Creating an assignment for a category, type or item that does not exist answers 404 (it used to store an assignment for nothing), and listing the items of an unknown type, or the assignments of an unknown category, type or item, answers 404 (it used to answer an empty list).

## Admin API: wrong or stale response bodies, and three decided rules (#419, response half)

Scripts and API keys that call the admin API can see these changes. Migration 000040 is new.

**Breaking**

- **Inactive statuses are retired for everyone.** `PATCH /tickets/{id}` with a `status_id` that names an inactive status now answers `409 status_inactive`, for staff, administrators and API keys alike; before, the move was accepted. Tickets already in an inactive status stay there and can move out, and sending a ticket the status it already holds is not a move. The ticket status picker no longer lists inactive statuses (the ticket's own status stays listed). If you deactivated `Pending` expecting SLA pausing to carry on, it now stops for new tickets; see the SLA section of DESIGN.md. The reopen target setting is not re-checked.
- **API keys: who lists and revokes which.** (Security: goes in the release notes under Security, PR 8.) An API key could delete another administrator's key if it knew the id; that has been possible since 1.0.0 (1.0.0 to 1.2.0 are affected) and is revoke-only. Now an API key lists and revokes only keys of its own owner (a key that is someone else's answers `404`). An administrator signed in through the browser lists and revokes **every** key, so `GET /admin/api-keys` for a browser session now returns all keys instead of the caller's own, and each entry carries `owner_name` and `owner_email`.
- **Custom-field errors have their own codes.** A name already in use (create or PATCH of a definition) and a field assigned twice to one place now answer `409 conflict` (they were `400 bad_request` with the database's own text). A `field_def_id` that names no field answers `400 invalid_reference`. A database failure on these routes is a `500 internal_error`, not a `400`. Validation errors stay `400 bad_request` with the same messages. No response carries database text any more.
- **A failed ticket count fails the request.** `GET /admin/statuses` and `GET /statuses` answer `500` when a count cannot be read; before, the status was reported with `ticket_count: 0`, and the admin page treats 0 as safe to hard-delete.
- **Category, type and item names are trimmed on `PATCH`**, as they are on create. A name sent with spaces around it is stored without them, and the response is the saved row. Names already stored are not changed.

**Additive or corrected**

- **`auth_type` reports OIDC.** New values: `oidc`, `local+oidc`, `saml+oidc` and `local+saml+oidc`. `local`, `saml` and `both` (password plus SAML) mean what they did. An account that signs in only through OIDC, or has a password and OIDC, used to read `local`. A client that switches on `auth_type` with a fixed list of values needs to handle the new ones.
- **`visible_on_new` on a new field assignment:** the API default is still false when omitted. The database column default (it was true) is now false too (migration 000040); existing rows are untouched.
- **`GET /admin/sla/policies` is ordered by severity** (critical, high, medium, low, then policies with no priority), then by name. It was ordered by the priority text, which put `low` before `medium`.
- **`POST /admin/oauth-clients` returns `id`** (the record id that `DELETE` takes), beside `client_id` and `client_secret`.
- **`PATCH /admin/users/{id}` returns the real `passkey_count`**; it always said 0.
- **`PATCH /admin/webhooks/{id}` returns the saved row.** After a URL change `last_delivery` is `null`; it showed the old receiver's result.
- **`PATCH /admin/custom-fields/{id}` returns the saved, trimmed row.** `POST` and `PATCH /admin/statuses` return the status's real `ticket_count`; they said 0.
- **A blank webhook `secret` on `PATCH` still clears it**, by design and for administrators only; now documented in the API spec and DESIGN.md.

**Ordering.** This change adds migration **000040**; #469 holds 000037, #440 000038 and #432 000039. It is built on #432 and must merge after it.
