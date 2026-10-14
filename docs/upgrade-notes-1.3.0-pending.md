# Upgrade Notes for v1.3.0 (Pending)

These notes document configuration and environment changes in v1.3.0. They will be folded into the release notes by PR #438.

## Breaking: Staff and Administrators Can No Longer File Into an Archived Category, Type or Item (#421)

Previously the server let staff and administrators create tickets under, and reclassify tickets into, categories and types an administrator had archived, and never checked items for anybody. That is reversed: **nobody, staff and administrators included, can newly pick an archived category, type or item.**

- `POST /api/v1/tickets`, `PATCH /api/v1/tickets/{id}` (with `category_id`/`type_id`/`item_id`) and the MCP `create_ticket` tool answer **400** (`bad_request`, for example `type_id is not an active type`) when the classification includes an archived node. This applies to sessions and API keys alike.
- A ticket **already** in an archived node keeps it. Unrelated edits, a PATCH that repeats the existing classification, and staff follow-ups of a closed ticket still work.
- Staff and administrators still see archived rows, now greyed out, labelled "(archived)" and not selectable in the new-ticket and reclassify pickers. A ticket's own current archived node stays selected so it can keep it.
- A PATCH that carries a refused classification now changes nothing: the status and assignee in the same request are no longer applied before the 400.

What to check: scripts or integrations that file or reclassify tickets as staff or admin into an archived node will start getting 400. Unarchive the node (Admin → Categories) or file into an active one.

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

## API Behaviour Changes (#421)

These affect scripts and integrations that call the API. Three can make a request that used to succeed now fail; the rest change what an answer says.

### Requests that now fail

- **Unknown settings keys are refused.** `PATCH /api/v1/admin/settings` with a key that is not a real setting now answers `400 unknown_setting`, names the key, and writes nothing (not even the valid keys in the same request). Before, any key was stored, so a typo such as `mfa_enabld` answered 204 and changed nothing. Fix: use the exact setting names from `GET /api/v1/admin/settings` (the `_set` presence flags are still refused with `readonly_setting`).
- **`POST /api/v1/tickets/{id}/resolve` with a body that is not valid JSON is `400`.** Before, it resolved the ticket and dropped the notes. An empty body still resolves with no notes.
- **`GET /api/v1/auth/verify-email?token=` is `422 token_invalid` for an address that has since gained an account.** Before, it answered 200 and only the POST refused.

### Answers that now say something different

- **`GET /api/v1/auth/providers`** is served as `application/json` (it had no content type). `oidc` and `saml` now mean "loaded and working", not "switched on": an identity provider that did not answer shows `false`, and SAML shows `true` whenever it is loaded, whatever `saml_enabled` says. `local` is still always `true`; the new `local_admin_only` is `true` when `saml_enabled` limits password sign-in to administrators.
- **`GET /api/v1/admin/oidc` and `/admin/saml`** gain `live`. `configured: true, live: false` means "saved, not working". `configured` is unchanged.
- **`GET /api/v1/auth/signup/status`**: `open_registration` is now what is in force, so it reads `false` when an allowed-domains list limits signup, even with the open-registration setting on. The new `restricted_by_domain_list` says why. Who can sign up is unchanged.
- **`Retry-After` on a `429`** from the sixty-second login, signup and guest limits is now the seconds left in the window (1 to 60) instead of always 60. The 15-minute MFA lock still says 900.
- **A server crash inside a request** answers the JSON `500 internal_error` body instead of an empty body.
- **Passkey sign-in and registration (`.../passkey/finish`, `.../register/finish`)** answer `500` when the server cannot write the session (they said `400 no_challenge`), and refuse a body over 64 KiB with `413 too_large`.
- **`POST /api/v1/me/mfa/enroll/confirm`** answers `500` when the server cannot save the factor (it said `400 invalid_code`). A wrong code and an enrolment that was never started are still `400 invalid_code`.
- **`POST /api/v1/guest/resend`** accepts an empty body (it was `400`). Malformed JSON is still `400`.
- **`DELETE /api/v1/tickets`** is `405` as before. Its `Allow` header carries `GET` and `POST` as two header lines, which is what the server always sent; clients that read only the first line saw `GET`.

## Old Password Hashes Must Be Reset After 30 Days (#453)

1.3.0 hashes passwords at bcrypt cost 12 (earlier releases used 10). An account is upgraded automatically the next time its owner signs in. **Accounts that have not signed in within 30 days of the first start of 1.3.0 must reset their password:** a correct password on an old hash is refused with 403 `password_reset_required` and the message "Your password was saved in an older, weaker form and must be reset before you can sign in." until it is reset.

- **Clock:** starts when 1.3.0 first runs against the database (migration 000044 records it once).
- **N:** Settings → Authentication → Passwords, default 30, 1 to 365. Changing it moves the deadline for everyone.
- **Exempt:** all administrators, so an instance cannot lock itself out. They are upgraded at their next sign-in like everyone else. Staff and requesters are refused. When SAML restricts password sign-in to administrators, others get the SAML refusal instead.
- **How people reset:** "Forgot password" on the sign-in page once password reset by email lands (#451), or an administrator sets a new password for them. Until then only the administrator route works.
- **What to check:** people who sign in rarely (seasonal staff, requesters) are the ones affected. Tell them, or have an administrator reset them, before day 30.
- **API clients:** sign-in with an old hash after the deadline answers 403 `password_reset_required` instead of 200. New installs are unaffected.
