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

## Ticket and Settings API Responses (#420)

Changes a script or integration may notice:

- `POST /tickets` now answers with the ticket after routing, so `assignee_user_id` / `assignee_group_id` are filled in when a routing rule or the auto-assign setting applied. Before, they were absent until the ticket was fetched again.
- `POST /tickets/{id}/replies` now includes `author_name` in the reply, as the reply list does.
- `PATCH /tickets/{id}` with no body at all is now the same as `{}`: nothing changes, and the answer is `200` with the ticket. Before, it was `400`. Malformed JSON is still `400`.
- `PATCH /admin/settings` now refuses JSON `null` (`400`) for `attachment_scan_address`, `attachment_allowed_types` and the four `attachment_reputation_*_enabled` toggles. Before, `null` was stored and read as "unset" or "off". To clear the scanner address, send an empty string. To restore the default allowed types, send the list you want.
- The six choice settings in `PATCH /admin/settings` (`closed_reopen_policy`, `audit_mask_requester_names`, `attachment_scan_policy`, `attachment_infected_handling`, `attachment_mismatch_handling`, `attachment_reputation_refresh`) now answer their own error code for every refusal: `invalid_closed_reopen_policy`, `invalid_audit_mask_requester_names`, `invalid_scan_policy`, `invalid_infected_handling`, `invalid_mismatch_handling`, `invalid_reputation_refresh`. Before, `null` and a value of the wrong type (a number, a boolean) answered `bad_request` for most of them. The status is still `400`, and the messages are unchanged. A script that matched on `bad_request` for these needs to match the specific code instead.
