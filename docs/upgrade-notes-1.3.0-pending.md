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

## Email: SMTP Encryption and Honesty (Breaking, Read First)

Email (SMTP) now fails loudly instead of quietly. Several setups that used to "work" now fail, and the failure shows in the admin panel. Check these before upgrading. Nothing needs changing if your mail server offers STARTTLS (or you use port 465) and, when `SMTP_USER` is set, offers AUTH.

There is no new setting. The rule depends on the port and on whether `SMTP_PASSWORD` is set:

- **Password set:** TLS is required. Port 465 uses TLS from the first byte; every other port must offer STARTTLS. A password is never sent unencrypted, even to a private address.
- **No password:** plain SMTP is allowed only when the server's address is loopback, private (10/8, 172.16/12, 192.168/16) or IPv6 fc00::/7. A public address still needs TLS.

### What breaks

1. **A localhost or private relay that took a password without TLS now fails.** Before, the password went out in the clear when the relay did not offer STARTTLS. Now the send fails with `SMTP server does not offer STARTTLS, and SMTP_PASSWORD is set: refusing to send the password unencrypted`. Fix: turn on STARTTLS (or TLS on port 465) on the relay, or drop the password if the relay does not need one for this network.
2. **Unauthenticated mail to a public server without STARTTLS now fails.** Before, mail went out in the clear. Now: `SMTP server does not offer STARTTLS and is not on a private address: refusing to send mail unencrypted`. Fix: use a server that offers STARTTLS or port 465.
3. **`SMTP_USER` set but the server offers no AUTH now fails the send.** Before, the server was used anonymously and the mail went out (or was rejected later). Now: `SMTP server does not offer AUTH but SMTP_USER is set`. Fix: point at a server that offers AUTH, or remove `SMTP_USER` and `SMTP_PASSWORD` if the relay accepts your mail without a login.
4. **Turning on signup or guest submission is refused while email is off.** `PATCH /admin/settings` answers `400 email_required` when `self_signup_enabled` or `guest_submission_enabled` goes from off to on and `SMTP_HOST` or `SMTP_FROM` is unset. Installs that already have either on are not switched off and are not refused when they save other settings; they get a warning in the admin panel instead.
5. **A broken email template now fails the message at once.** Before, the message was dropped and nothing said so. Now the row is marked failed with no retries and the error appears in the admin email warning. (The shipped templates render; this only matters for a damaged build.)

Port 465 now connects with TLS from the first byte. Before, it hung until the timeout, so no working setup changes.

### How you will see it

Failed sends are retried for about an hour (a template error is not retried) and then marked failed. While any email message is retrying or failed within the last day, `GET /api/v1/admin/security-warnings` returns `email.failing: true` with the newest error text (addresses replaced by `[address]`). It also lists which of `SMTP_HOST` and `SMTP_FROM` is unset, and whether signup or guest submission is on with email off. The server log says once at startup whether email is enabled, and warns when only one of `SMTP_HOST` and `SMTP_FROM` is set.
