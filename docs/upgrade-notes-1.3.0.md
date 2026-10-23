# Upgrade Notes for v1.3.0: API, Integration and Admin Changes

These notes cover the API, integration and admin changes in this release. They are not the complete list of what is new: the website upgrade page is. Read **Breaking changes** before you upgrade: they list what existing scripts, integrations and receivers will see fail or change. **Security** lists the fixes that make the upgrade advisable. **New features** and **Changed behaviour** describe the rest, and **Upgrade steps** (at the end) gives the order to work in. The complete upgrade page, including the changes to the web interface, is on the website: https://gohelpdesk.org/docs/upgrading-1.3.0.

## Breaking changes (read first)

Each item below either makes a request that used to succeed fail, or changes what a script, receiver or user sees. The sections that follow give the detail and the fix.

### Nobody Can Newly File Into an Archived Category, Type or Item (#421)

Previously the server let staff and administrators create tickets under, and reclassify tickets into, categories and types an administrator had archived, and never checked items for anybody. That is reversed: **nobody, staff and administrators included, can newly pick an archived category, type or item.**

- `POST /api/v1/tickets`, `PATCH /api/v1/tickets/{id}` (with `category_id`/`type_id`/`item_id`) and the MCP `create_ticket` tool answer **400** (`bad_request`, for example `type_id is not an active type`) when the classification includes an archived node. This applies to sessions and API keys alike.
- A ticket **already** in an archived node keeps it. Unrelated edits, a PATCH that repeats the existing classification, and staff follow-ups of a closed ticket still work.
- Staff and administrators still see archived rows, now greyed out, labelled "(archived)" and not selectable in the new-ticket and reclassify pickers. A ticket's own current archived node stays selected so it can keep it.
- A PATCH that carries a refused classification now changes nothing: the status and assignee in the same request are no longer applied before the 400.

What to check: scripts or integrations that file or reclassify tickets as staff or admin into an archived node will start getting 400. Unarchive the node (Admin → Categories) or file into an active one.

### Old Password Hashes Must Be Reset After 30 Days (#453)

Passwords are now stored more strongly (bcrypt cost 12; earlier releases used 10). Each password is upgraded automatically the next time its owner signs in. **Anyone who has not signed in by 30 days after you first start 1.3.0 (change this under Settings → Authentication → Passwords) must reset their password before they can sign in again, using Forgot password or with an administrator's help.** Administrators are never blocked. A correct password on an old hash is refused with 403 `password_reset_required` and the message "Your password was saved in an older, weaker form and must be reset before you can sign in." until it is reset. With email on, that refusal also emails the person the same reset link "Forgot password" sends, and the message says to check their email; with email off it says to ask an administrator.

- **Clock:** starts when 1.3.0 first runs against the database (migration 000044 records it once).
- **N:** Settings → Authentication → Passwords, default 30, 1 to 365. Changing it moves the deadline for everyone.
- **Exempt:** all administrators, so an instance cannot lock itself out. They are upgraded at their next sign-in like everyone else. Staff and requesters are refused. When SAML restricts password sign-in to administrators, others get the SAML refusal instead.
- **How people reset:** "Forgot password?" on the sign-in page (see *Forgot Password by Email (#451)* under New features; it needs email), or an administrator sets a new password for them. With email off only the administrator route works.
- **What to check:** people who sign in rarely (seasonal staff, requesters) are the ones affected. Tell them, or have an administrator reset them, before day 30.
- **API clients:** sign-in with an old hash after the deadline answers 403 `password_reset_required` instead of 200, and, when the server can send email, mails the person a reset link (limited per address like "Forgot password"). New installs are unaffected.

### Email: SMTP Encryption and Setup Rules

Email (SMTP) now fails loudly instead of quietly. Several setups that used to "work" now fail, and the failure shows in the admin panel. Check these before upgrading. Nothing needs changing if your mail server offers STARTTLS (or you use port 465) and, when `SMTP_USER` is set, offers AUTH.

There is no new setting. The rule depends on the port and on whether `SMTP_PASSWORD` is set:

- **Password set:** TLS is required. Port 465 uses TLS from the first byte; every other port must offer STARTTLS. A password is never sent unencrypted, even to a private address.
- **No password:** plain SMTP is allowed only when the server's address is loopback, private (10/8, 172.16/12, 192.168/16) or IPv6 fc00::/7. A public address still needs TLS.

#### What breaks

1. **A localhost relay that took a password without TLS now fails.** Before, the password went out in the clear to localhost when the relay did not offer STARTTLS (Go already refused to send a password in the clear to any other non-localhost relay). Now the send fails with `SMTP server does not offer STARTTLS, and SMTP_PASSWORD is set: refusing to send the password unencrypted`. Fix: turn on STARTTLS (or TLS on port 465) on the relay, or drop the password if the relay does not need one for this network.
2. **Unauthenticated mail to a public server without STARTTLS now fails.** Before, mail went out in the clear. Now: `SMTP server does not offer STARTTLS and is not on a private address: refusing to send mail unencrypted`. Fix: use a server that offers STARTTLS or port 465.
3. **`SMTP_USER` set but the server offers no AUTH now fails the send.** Before, the server was used anonymously and the mail went out (or was rejected later). Now: `SMTP server does not offer AUTH but SMTP_USER is set`. Fix: point at a server that offers AUTH, or remove `SMTP_USER` and `SMTP_PASSWORD` if the relay accepts your mail without a login.
4. **Turning on signup or guest submission is refused while email is off.** `PATCH /admin/settings` answers `400 email_required` when `self_signup_enabled` or `guest_submission_enabled` goes from off to on and `SMTP_HOST` or `SMTP_FROM` is unset. Installs that already have either on are not switched off and are not refused when they save other settings; they get a warning in the admin panel instead.
5. **A broken email template now fails the message at once.** Before, the message was dropped and nothing said so. Now the row is marked failed with no retries and the error appears in the admin email warning. (The shipped templates render; this only matters for a damaged build.)

Port 465 now connects with TLS from the first byte. Before, it hung until the timeout, so no working setup changes.

#### How you will see it

Failed sends are retried for about an hour (a template error is not retried) and then marked failed. While any email message is retrying or failed within the last day, `GET /api/v1/admin/security-warnings` returns `email.failing: true` with the newest error text (addresses replaced by `[address]`). It also lists which of `SMTP_HOST` and `SMTP_FROM` is unset, and whether signup or guest submission is on with email off. The server log says once at startup whether email is enabled, and warns when only one of `SMTP_HOST` and `SMTP_FROM` is set.

### API keys and OAuth clients can no longer do what only a person should (#450)

These come with the new staff API key feature (see *Staff Can Create Their Own API Keys (#450)* under New features).

- **API keys can no longer create API keys. This breaks scripts that use a key to make keys.** Only a signed-in administrator (or, for their own, signed-in staff) creates a key. `POST /api/v1/admin/api-keys` called with an API key now answers 403 `session_required`; an OAuth client answers 403 `forbidden`. A script that rotated its own key by calling this route with the old one must be changed: create the next key in the web UI (or through a signed-in session) instead. Listing and revoking keys with a key still works, but a key now lists and revokes only itself (see Security). **The same goes for OAuth clients:** `POST /api/v1/admin/oauth-clients` called with an API key now answers 403 `session_required` (an OAuth client, 403 `forbidden`); create clients in the web UI. A key can still list and delete clients.
- **API keys and OAuth clients can no longer write users, or change group membership. This breaks scripts that do.** Keys may only READ users. These now answer 403 `session_required` for an API key (403 `forbidden` for an OAuth client): `POST /api/v1/admin/users`, `PATCH /api/v1/admin/users/{id}`, `DELETE /api/v1/admin/users/{id}`, `POST /api/v1/admin/users/{id}/password`, `POST` and `DELETE /api/v1/admin/groups/{id}/members…` and `POST` and `DELETE /api/v1/admin/groups/{id}/scopes`. That covers provisioning scripts (create), offboarding scripts (disable, delete, remove from groups), directory sync (rename, email, role, group membership), password and MFA resets, and anything that gives a group access to categories. What to do instead: an administrator signed in through the web UI makes the change, or use single sign-on just-in-time provisioning (SAML or OIDC creates the account at first sign-in; note it creates only requester-role accounts, so staff still need a signed-in administrator to promote them). Listing users and groups with a key is unchanged. Why: a key that can write a user can make itself a person: set a password, promote an account it knows the password of, or re-address an account for a reset link, then sign in and create API keys that outlive the key.
- **Maximum lifetime: this changes `POST /admin/api-keys` for existing scripts.** While the limit is a number of years, `expires_at` is now **required** (400 `expires_at_required`) and may be no later than that many years from now (400 `expires_at_too_far`). A script that creates keys without `expires_at` needs to send one, or an administrator must choose Never. Keys made before the upgrade keep working. The limit is applied when a key is created, not when it is used.
- **Three more settings are session-only:** `ticket_scope_enforced`, `auto_assign_group_id` and `auto_assign_user_ids` now answer 403 `session_required` to an API key or OAuth client, because they decide who sees and who is handed tickets. A script that sets them with a key must be changed to a signed-in administrator's change.
- **Webhooks made by a key stop when the key does.** A webhook that an API key created, or last changed the URL of, is paused once that key is revoked or has expired: it shows as Paused (with the reason) on Admin → Webhooks and receives nothing until a signed-in administrator clicks Re-approve (`POST /api/v1/admin/webhooks/{id}/reapprove`, session only). Webhooks that exist today, and any made or last re-pointed in the web UI, are not tied to a key and are unaffected. A key whose owner is disabled or deleted counts as gone too. While a hook is paused an API key cannot edit it (403 `webhook_paused`); a signed-in administrator re-approves it, or changes its URL, first. A script that creates webhooks with a short-lived key should expect them to pause.
- **`users:write` is no longer offered.** The scope catalogue and the pickers leave it out, and a new key can no longer be given it (400 `invalid_scope`). Keys that already hold it keep it; it opens nothing, since keys can't write users.

### An IP Address, Single-Label or Non-ASCII `BASE_URL` Disables Passkeys (#494)

Passkeys are bound to a domain name, so they work only when the host in `BASE_URL` is a domain name (`help.example.com`) or `localhost`. **If `BASE_URL` is an IP address (`http://192.168.1.10:8080`, `http://[::1]:8080`), a single-label host (`http://helpdesk:8080`) or contains non-ASCII characters, passkeys are disabled.** The server still starts. It logs `passkeys unavailable` once at startup, and administrators see a warning banner explaining that `BASE_URL` must be a domain name.

- `GET /api/v1/me/passkeys`, `POST /api/v1/me/passkeys/register/*` and `POST /api/v1/auth/local/passkey/*` answer `503 passkeys_unavailable`.
- `GET /api/v1/auth/providers` gains `passkeys` (`false` here) and `GET /api/v1/admin/security-warnings` gains `passkeys: {available, reason}`.
- An account whose only second factor is a passkey **cannot sign in** while passkeys are off: sign-in answers `403 passkey_unavailable` and tells the person to ask an administrator to reset their sign-in factors. An administrator's Reset MFA clears the passkey, after which the person signs in and enrols an authenticator app (TOTP). Accounts with TOTP are unaffected. This includes the last administrator: run `docker compose exec app /app/server reset-factors <email>` (from the `docker/` directory) on the server to clear their factors. Passkeys registered under a domain `BASE_URL` that is later fixed work again if the domain matches.

What to check: if your instance is on an IP or single-word host and you want passkeys, give it a DNS name and set `BASE_URL` to it. Changing the domain invalidates passkeys registered under the old one.

### API requests that now fail (#421)

These affect scripts and integrations that call the API. Three can make a request that used to succeed now fail:

- **Unknown settings keys are refused.** `PATCH /api/v1/admin/settings` with a key that is not a real setting now answers `400 unknown_setting`, names the key, and writes nothing (not even the valid keys in the same request). Before, any key was stored, so a typo such as `mfa_enabld` answered 204 and changed nothing. Fix: use the exact setting names from `GET /api/v1/admin/settings` (the `_set` presence flags are still refused with `readonly_setting`).
- **`POST /api/v1/tickets/{id}/resolve` with a body that is not valid JSON is `400`.** Before, it resolved the ticket and dropped the notes. An empty body still resolves with no notes.
- **`GET /api/v1/auth/verify-email?token=` is `422 token_invalid` for an address that has since gained an account.** Before, it answered 200 and only the POST refused.

### Webhook receivers (#448)

Two changes can break a receiver. Retries, delivery ids and history are under Changed behaviour and New features.

#### The raw payload is now ids only

A hook with payload format **Raw JSON** is sent only the event name (`Type`), `TicketID`, `ActorID`, `OccurredAt` and, where the event has them, a `Payload` with `new_status_id` or `target_id`. These are gone from the payload: `TrackingNumber`, `Subject`, `Priority`, `guest_email`, `reporter_email`, `ReplyBody`, and the `internal` and `link_type` fields. A receiver that reads any of them must fetch the ticket through the REST API using `TicketID`. Whether something is a reply or a note is now the event name (below).

Slack, Teams, Discord and Jira Automation hooks are not affected: their messages still carry the subject, tracking number, status and public reply text, and never an address or a note's text.

#### Internal notes are their own event

`ticket.replied` is now sent for customer-visible replies only. Internal notes are sent as the new `ticket.note_added`. A hook subscribed to `ticket.replied` stops receiving notes; add `ticket.note_added` (or choose all events) to keep them.

### Requesters see less of their tickets (#464)

- **Requesters no longer see the activity history.** `GET /tickets/{id}/audit` and `GET /tickets/{id}/history` now answer a reporting user `403`; before, a requester could read the audit feed (with assignment actors withheld) and the status timeline of their own ticket. The ticket page no longer shows them status-change events in the conversation either. Requesters see the ticket's current state and its public replies, as before. Staff and administrators are unchanged, and guests never had either route. A script that signs in as a requester and reads those two routes will now get `403`.
- **A requester now receives only an allowed list of ticket fields**, in every ticket response including MCP and the follow-up. They get `id`, `tracking_number`, `subject`, `description`, `status_id`, `priority`, `category_id`, `type_id`, `item_id`, `created_at`, `resolved_at`, `assignee_user_id` (kept as an id) and `resolution_notes`. **They no longer receive** `assignee_group_id`, `reporter_user_id`, `guest_email`, `guest_name`, `guest_phone`, `closed_at`, `updated_at`, `pending_since`, `sla_paused_seconds` or `reopen_count`. A script that signs in as a requester and reads those keys will find them gone; the ticket page does not read them. A field added to a ticket in a later release stays hidden from requesters until it is added to the list. Guests no longer receive `updated_at` (they had it); the rest of their view is unchanged. Staff and administrators are unchanged except that `reopen_count` is omitted when 0.

### Admin API: inactive statuses, custom-field codes, ticket counts, names (#419)

- **Inactive statuses are retired for everyone.** `PATCH /tickets/{id}` with a `status_id` that names an inactive status now answers `409 status_inactive`, for staff, administrators and API keys alike; before, the move was accepted. Tickets already in an inactive status stay there and can move out, and sending a ticket the status it already holds is not a move. The ticket status picker no longer lists inactive statuses (the ticket's own status stays listed). If you deactivated `Pending` expecting SLA pausing to carry on, it now stops for new tickets; see the SLA section of DESIGN.md. If the configured reopen target status is inactive, every reopen (forced reopen, a requester's or guest's reply on a Resolved ticket) goes to New instead, administrators see a banner (`reopen_target` in `GET /admin/security-warnings`) asking for an active status, and the `PATCH /admin/statuses/{id}` that deactivates the target returns a `warning`.
- **Custom-field errors have their own codes.** A name already in use (create or PATCH of a definition) and a field assigned twice to one place now answer `409 conflict` (they were `400 bad_request` with the database's own text). A `field_def_id` that names no field answers `400 invalid_reference`. A database failure on these routes is a `500 internal_error`, not a `400`. Validation errors stay `400 bad_request` with the same messages. No response carries database text any more.
- **A failed ticket count fails the request.** `GET /admin/statuses` and `GET /statuses` answer `500` when a count cannot be read; before, the status was reported with `ticket_count: 0`, and the admin page treats 0 as safe to hard-delete.
- **Category, type and item names are trimmed on `PATCH`**, as they are on create. A name sent with spaces around it is stored without them, and the response is the saved row. Names already stored are not changed.

- **API keys list and revoke only themselves.** See *API key delete is scoped to its owner* under Security.

### Guests Can No Longer Upload Until You Turn It On (#445)

Before this release a guest could attach a file through `POST /api/v1/guest/attachments` whenever guest submission was on. **That is now off by default, including on an upgraded install.** A new setting, **Guests may attach files** (`guest_attachments_enabled`, Admin → Settings → General → Submissions, under guest submission), controls it. Until an administrator turns it on, the route answers the generic `404 not_found` a bad link gets, and a script that uploads as a guest will see that.

- **Dependent on guest submission.** It only takes effect while guest submission is on.
- **Per-file limit.** `guest_attachment_max_mb`, a slider from 1 to 100 MB, 5 MB by default. A guest is held to the lower of it and the instance-wide 25 MB maximum. Over the limit is `413 too_large`, and the message names the limit.
- **Two more limits, with small defaults.** `guest_attachment_max_per_ticket` (1 to 50, default 5) caps the files guests can attach to one ticket, and `guest_attachment_uploads_per_hour` (1 to 30, default 3) caps uploads per guest link per hour. Over the cap is `409 guest_attachment_limit`; over the rate is `429 rate_limited` with `Retry-After`. Both are on the same settings page, under the switch. The rate is in memory and per process, so a restart clears it. A refusal decided before the scan (wrong type, too big) does not use up the guest's hour; anything decided after it (not an image, a malware verdict) or a server fault does. Refused attempts also share a fixed 20-per-hour budget per ticket.
- **Session only.** All of these settings need a signed-in administrator; an API key or OAuth client sending either is refused with `403 session_required`.
- **The guest page now has an Add files button** while the setting is on. Uploads get the same type, scanning and reputation rules as everyone else's, and are download-only. The submission form (`/submit`) has none: the link to attach from is emailed once the ticket exists.
- **`GET /api/v1/site`** gains `guest_attachments_enabled` and, while it is on, `guest_attachment_max_bytes`, `guest_attachment_max_per_ticket` and `guest_attachment_uploads_per_hour`.
- **Migration 000054** (runs on start, nothing to do) adds `attachments.uploaded_by_guest`, default false, so the cap counts only guest uploads. Files guests attached before the upgrade are not counted toward it. The settings themselves need no migration; they are read with their defaults.

What to check: if your guests attach files today, turn the setting on after you upgrade.

### `GET /users/lookup` is closed to API keys and OAuth clients (#290)

`GET /api/v1/users/lookup` now answers `403 session_required` to an API key or an OAuth client, whatever scopes it holds. In the 1.3.0 betas it opened to any credential with `users:read`, which a staff key could be given, so a key could search the user directory by name or address. It is for the New ticket page's requester picker, in a signed-in session, and nothing else. Staff keys can no longer be given `users:read` (it opened nothing else for them); keys that already hold it keep it and it does nothing.

What to do: a script that filed tickets for other people by looking the person up first sends `reporter_email` on `POST /api/v1/tickets` (or MCP `create_ticket`) and lets the server resolve it: the ticket goes to the account with that address if there is one, otherwise it is a guest ticket (`guest_name` required, guest submission on). `reporter_user_id` still works for ids you already have. An administrator's key that searched accounts by name or address can use `GET /api/v1/admin/users?page=1&q=` (read-only, `users:read`; `q` applies only with `page`); staff keys have no replacement.

### `PATCH /api/v1/me/password` now needs `current_password` (#555)

In 1.2.0, `PATCH /api/v1/me/password` took `password` alone and did not ask for the current password. 1.3.0 requires `current_password` and refuses a missing or wrong one with `400 invalid_current_password`, so a 1.2.0 script that sends only `password` now fails. The new name for the replacement is `new_password`; `password` is still read when `new_password` is absent. Wrong guesses are counted per account against a budget the size of sign-in's (`AUTH_RATE_LIMIT_PER_MINUTE`, a separate counter) and answer `429 rate_limited` once it is spent; the correct password is still accepted. A `401` from this route means there is no session. The account page shows a wrong current password inline and keeps the person signed in.

### Also breaking, described elsewhere

- A ticket-changing webhook delivers one event per change, and a receiver that switches on `Type` can miss a kind: see *One Change, One Event* under Changed behaviour.
- If `JWT_SECRET` is unset or short, OAuth clients must fetch a new access token once: see *JWT Secret* under Security.
- MCP `add_reply` now emails the requester: see *MCP `add_reply`* under Changed behaviour.
- `PATCH /admin/settings` null and error-code changes: see *Ticket and Settings API Responses (#420)* under Changed behaviour.

## Optional weekly check for new releases (off unless you choose it)

1.3.0 can check whether a newer release of Go Help Desk exists. **It is off.** It stays off after you upgrade, and nothing contacts GitHub unless an administrator turns it on.

- **What is sent.** One `GET` to GitHub's public releases API (`https://api.github.com/repos/PubliciaLLC/go-help-desk/releases/latest`) with exactly two request headers of its own: `User-Agent: go-help-desk` (a fixed name, no version number) and `Accept: application/vnd.github+json`. No query string, body, cookie, instance identifier or usage data, and nothing about your server or version. GitHub sees your server's IP address, as any website would.
- **How often.** At most once every 7 days, and not again after a restart until the week is up. A failed check (GitHub down, no route out) is logged once and tried again the following week.
- **What you see.** If a newer stable release exists, administrators (only) see a small dismissible line, "Version X is available (you run Y)", linking to the release. Nothing is downloaded or installed. Prereleases and drafts are ignored.
- **Air-gapped installs.** Leave it off. While it is off the server makes no request.
- **Where to choose.** On a new install, the setup screen asks (unchecked: "Check for new releases (contacts GitHub once a week; sends nothing about your server)"). On an upgraded install, **the first administrator who signs in after the upgrade is taken once through a short "After upgrading to 1.3.0" wizard**, and this is its first question. The wizard then asks about staff email notifications (the upgrade turned them off), staff API keys and their lifetime, the old-password reset window, and (only if guest submission is on) guest file uploads, which 1.3.0 switched off, showing today's setting for each. Skip any step to leave that setting as it is; finishing or skipping the wizard means it is not shown again. You can change the update check later under Settings → General → Updates.

## Security

### Cross-site request forgery on the MCP routes (1.1.1 to 1.2.x), GHSA-3jmq-7vm4-cc7p

A web page on a sibling site could send MCP requests that the server accepted under a signed-in staff member's session. 1.3.0 refuses any MCP request whose Origin is not your `BASE_URL`, and binds each SSE session to the user and credential that opened it. Upgrade if you expose MCP. Advisory: https://github.com/PubliciaLLC/go-help-desk/security/advisories/GHSA-3jmq-7vm4-cc7p. Detail is in the two MCP sections below.

### API key delete is scoped to its owner (#450)

An API key could delete another administrator's key if it knew the id. That has been possible since 1.0.0 (1.0.0 to 1.2.0 are affected). It is revoke-only, needs an administrator's key and the other key's id, and is low severity; upgrading is advised. A GitHub security advisory is published with this release.

**TODO BEFORE TAGGING: replace the placeholder link below with the real advisory.** The release build refuses to run while the advisory placeholder below is still there.

https://github.com/PubliciaLLC/go-help-desk/security/advisories/GHSA-TODO
<!-- GHSA id: Erik fills in on tag day -->

Now an API key lists and revokes **only itself**: not its owner's other keys and not anyone else's (any other id answers `404`). An administrator signed in through the browser lists and revokes **every** key, so `GET /admin/api-keys` for a browser session now returns all keys instead of the caller's own, and each entry carries `owner_name` and `owner_email`. A script that used one key to list or revoke the others now needs a browser session.

### MCP: Requests From Another Origin Are Refused (#460)

Every MCP route (`/mcp`, `/mcp/sse`, `/mcp/message` and anything else under `/mcp/`) now answers `403 origin_not_allowed` when the request carries an `Origin` header that is not your `BASE_URL`'s origin, before authentication is looked at. Requests with no `Origin` header, which is what SDKs, editors and `curl` send, are unaffected.

- A browser-based MCP client served from a different origin, or a reverse proxy that injects an `Origin` header, is now refused. Serve the client from `BASE_URL`'s origin or stop the proxy adding the header.
- 1.1.1 to 1.2.x accepted these requests on the SSE routes. This closes a path by which a web page on a sibling site could act through a signed-in user's session cookie. Upgrade if you expose MCP over SSE.
- A `BASE_URL` with a non-ASCII host does not stop the server: passkeys are turned off (see the passkeys section under Breaking changes). Write it in punycode if you want them. Browser requests to the MCP routes that carry an `Origin` are refused (`403 origin_not_allowed`) while `BASE_URL` has a non-ASCII host, because no origin can be built from it; requests with no `Origin` header are unaffected.

### MCP: Request Body Cap and SSE Sessions Bound to Their Actor (#481, #495)

- **MCP request bodies are capped at 1 MiB** on `/mcp`, `/mcp/` and `/mcp/message`. Over the cap the server answers **413** (`request_too_large`). Before, there was no cap. A client sending a single tool call larger than 1 MiB (a reply body of over a million characters; ticket descriptions are already capped at 50,000 bytes) is now refused; nothing in normal use comes near it.
- **An SSE session now belongs to the user and credential that opened the stream.** A `POST /mcp/message` for a `sessionId` from a different user, a different API key of the same user, or a browser session where the stream was opened with a key (or the reverse), is refused with **403** and not processed. An unknown, missing or closed `sessionId` gets the same 403. Clients that open the stream and post with the same credential (every ordinary MCP client) are unaffected. A client that opens the stream with one credential and posts with another must use one.

### JWT Secret (`JWT_SECRET`)

`JWT_SECRET` is now **optional**:

- If **unset or under 32 bytes**: A random 32-byte key is generated once and stored in the database. All instances in the cluster read the same key from the database.
- If **32+ bytes**: The environment value is used as-is (the long value wins over the generated key).
- If it is **exactly one of the example secrets published in this repository** (for example `change-me-in-production-this-is-really-insecure`): it is discarded, a generated key is used, and the server logs one warning. Anyone can read the example, so it must never sign anything. (`SESSION_SECRET` is different: an example value there is still flagged in the admin panel, not discarded.)
- **Upgrade path**: If you set `JWT_SECRET` to a long value in `.env`, it remains in use and nothing changes. If you unset it or provide a short value, a new key is generated on first boot. **OAuth clients must fetch a new access token once** (sessions are unaffected; they use `SESSION_SECRET`).
- **Backup implications**: If the server generates its JWT signing key (`JWT_SECRET` unset, shorter than 32 bytes, or an example value), the key is stored in the database, so every backup contains it: protect backups accordingly. Restoring a backup onto another instance carries the key with it (tokens remain valid there).
- **Rotation**: To rotate the key, delete the row from `instance_secrets` and restart. All OAuth tokens issued with the old key become invalid.

## New features

### Staff and Administrators Can File a Ticket for a Requester (#444)

`POST /api/v1/tickets` takes `reporter_user_id` (an existing account) or `reporter_email` (a typed address), **staff and administrators only**, at most one. The New ticket page shows them a **Requester** choice: Me (the default), Another user (search) or Email address. Requesters see no such control, and a requester who sends either field is refused with `403 forbidden`.

- **A typed address sends the guest link to that address.** Unless the address belongs to an account (then the ticket is filed for that account), it is filed exactly like a guest ticket: the person is emailed the link to read and reply, as for a ticket they submitted themselves. It is refused with `400` while guest submission is off, because the link would not open. As on the guest form, a **name (`guest_name`) is required with every `reporter_email`** (`400` without one), whether or not the address has an account, so a refusal says nothing about the address; it is ignored for an account. A phone number is optional.
- **A staff member can use `reporter_email` 40 times an hour**, then `429 rate_limited` (with `Retry-After`) and nothing is filed. It bounds how fast anyone can learn which addresses have accounts, and how many strangers get mail. Every `reporter_email` that passes validation counts, account or not, and the refusal for guest submission being off counts too; a request refused by validation (subject, category, type, item, address form, missing name) does not, and a unit, once taken, is never given back (a create that then fails costs it). Picking a user by `reporter_user_id` does not count. It is per staff member (an API key or OAuth client counts against the user it acts as), kept in memory, so it resets when the server restarts and each replica of a multi-instance deployment counts on its own. Not configurable.
- **An existing user who is filed for is emailed.** Someone picked from the list, or a typed address that matches an account, gets the ordinary "We have received your ticket" email with a link to the ticket, through the notification outbox. A member of staff filing their own ticket is not emailed, as before.
- The member of staff is the actor in the ticket's history and audit entry; the requester is its reporter or guest. **MCP `create_ticket` with `reporter_user_id` now does the same**: it used to record the reporter as the actor of a ticket the caller filed for them. It also emails an account filed for this way, as REST does. Creating your own ticket over MCP is unchanged.
- **Staff see a Requester line** on the ticket page (the account's name and email, or the guest's), on every ticket. `GET /api/v1/tickets/{id}` carries it as `requester` for staff and administrators only; requesters do not receive it.
- New `GET /api/v1/users/lookup?q=` feeds the picker: staff and administrators **in a signed-in session only** (an API key or OAuth client gets `403 session_required`; see Breaking changes), at most ten results (`id`, `display_name`, `email`), `400` for a query under two characters. Disabled accounts are not offered (MCP `create_ticket` still accepts one by id).
- No migration. Existing callers that send neither field behave exactly as before.

- **MCP `create_ticket` takes `reporter_email` too** (with `guest_name`, required, and `guest_phone`), by the same code as REST: an account if the address has one, otherwise a guest ticket, with the same 40-an-hour budget (one budget per staff member across REST and MCP), the same mail and the same actor. `reporter_user_id` still works.

What to check: an API key that files tickets does not need `users:read` to use `reporter_user_id` or `reporter_email`, and cannot use the lookup at all.

### Markdown in Ticket Text (#443)

Descriptions, replies and internal notes can now be written in Markdown. **Text written before 1.3.0 stays plain text.** 1.2 promised Markdown but never rendered it, so nothing already in the database is reinterpreted: every existing description and reply is marked `plain` (migration 000055) and is shown exactly as before. A reader can choose **Show formatted** on any plain post to see it through the new renderer. That changes only what that reader sees; nothing is stored and the default stays plain.

- **Who writes which format.** The web app's editor writes Markdown. **REST is unchanged unless it opts in:** `POST /tickets`, `/tickets/{id}/replies`, `/guest/tickets` and `/guest/replies` take an optional `format` (`plain` or `markdown`) and default to `plain`. **MCP `create_ticket` and `add_reply` default to `markdown`**, and their tool descriptions tell the agent to escape characters it means literally; pass `format: "plain"` to keep plain text. Any other value (`""`, `"Markdown"`, `"html"`, a number) is `400 invalid_format` on REST and a tool error on MCP, and nothing is written.
- **Reading.** `description_format` is on every ticket object and `body_format` on every reply, for staff, requesters and guests alike. A follow-up copies its original's format.
- **What is rendered.** CommonMark plus GitHub tables, strike-through and bare-URL links. No images (shown as the link "[image: alt]" and never loaded), no raw HTML (shown as text), headings all one small size, links http, https and mailto only. Code in a fenced block is highlighted for 13 languages. Markdown post readers get a **Plain text** button to see the source.
- **Webhooks, email and chat are unchanged.** A webhook payload carries the Markdown source as text; chat targets escape it as before; email carries no ticket text.
- **Migration 000055** adds `tickets.description_format` and `ticket_replies.body_format` (`plain` or `markdown`, no default afterwards). It does not rewrite the tables, but **stop every old (1.2 or earlier) instance before the migration runs**: the migration removes the column default, so an instance still running the old version fails every ticket create and reply. There is no rolling upgrade across this version.

What to check: an integration that already sends Markdown-looking text over REST keeps getting plain text unless it adds `"format": "markdown"`. An MCP agent that sent text with `*`, `#` or `_` meant literally will now have it formatted; tell it to escape them or pass `format: "plain"`.

### Replies Are Limited to 50,000 Bytes (#443)

A reply or internal note over **50,000 bytes of text** (not characters: letters outside basic English and emoji take 2 to 4 bytes each) is now refused with `400 body_too_long`, and nothing is written. It is the limit a ticket description has always had. It applies to `POST /tickets/{id}/replies`, `POST /guest/replies` and MCP `add_reply`. Before 1.3.0 a reply had no cap other than the 1 MiB request size, and a megabyte of text slowed every page that showed it.

- **Text already stored is untouched.** A longer post written before the upgrade is shown collapsed (its first 5,000 characters, a note, and a **Show full text** button). The button shows all of it, exactly as stored.
- **Scripts and integrations** that post long logs as replies will start getting 400. Split them, or attach them as a file.
- The web forms count in the same bytes, show a counter near the limit, and will not send more.

What to check: any script that pastes command output, a log or a stack trace into a reply.

### `unlock <email>` on the Server Console (#497)

`docker compose exec app /app/server unlock <email>` is the way back for a locked-out account, and it works when the web path cannot: a lost authenticator or passkey, a disabled account (the sole administrator included), a spent MFA lockout, or a federated administrator with no usable password. In one transaction it clears every second factor, re-enables the account, clears the MFA lockout, sets a new random password and prints it once, and ends all sessions. The password must be changed at the next sign-in.

- New migration `000045_must_change_password` adds `users.must_change_password` (default false). Nothing changes for existing accounts.
- `POST /api/v1/auth/local/login` gains `password_change_required` and `User` gains `must_change_password`. A session in that state is answered `403 password_change_required` everywhere except `PATCH /api/v1/me/password`.
- `reset-factors` is unchanged.

### Staff Can Create Their Own API Keys (#450)

API keys can now be made by staff, issued by an administrator on a staff member's behalf, and every new key has a maximum lifetime. **Two new settings**, both under Settings → Authentication → API keys and both session only (an API key or OAuth client cannot change them):

| Setting | Key | Default | Values |
|---|---|---|---|
| Staff may create their own API keys | `staff_can_create_api_keys` | off | true / false |
| Longest an API key may live | `api_key_max_lifetime_years` | 2 | integer 1 to 5 (years), or `-1` for Never; the settings page shows a slider with those six stops and a red warning above 2 |

- **Who:** administrators always. Staff only while the first setting is on. Requesters never own keys: `/me/api-keys` is 403 for them and an administrator cannot issue one for them (400 `requester_cannot_own_key`). There is no per-user permission.
- **Turning staff creation off** only stops new keys. Keys that already exist keep working, and staff can still list and revoke their own.
- **Where:** Account → API keys (list, create with an expiry, revoke), and `GET/POST /api/v1/me/api-keys`, `GET /api/v1/me/api-keys/scopes`, `DELETE /api/v1/me/api-keys/{id}`. Session only: an API key or OAuth client gets 403 `session_required`, so a key cannot create more keys.
- **Issuing on behalf:** `POST /api/v1/admin/api-keys` takes an optional `user_id`, from a signed-in administrator only. They can issue for a staff member or for themselves. Not for another administrator (400 `cannot_issue_for_administrator`), a requester, or a service account (400 `service_account_cannot_own_key`). Responses now include `user_id` and `created_by`.
- **Changing someone's role keeps their keys.** A key acts within its owner's current role. Promoting a staff member who holds an administrator-issued key leaves an administrator holding it; revoke it if that is not wanted.
- **A key's scopes never exceed its owner's role.** Staff keys can carry `tickets:read|write`, `groups:read` and `audit:read` (`users:read` is no longer offered: its one staff route, the requester lookup, is session-only); an administrator's any scope. Anything else is 400 `invalid_scope`, on both routes. `GET /api/v1/admin/scopes?role=staff` lists what a staff key may carry.
- **Marked for rotation:** both key lists carry `beyond_limit`. A key with no expiry, or one beyond the current limit (for example after the limit is lowered), is shown as "No expiry (longer than the current limit)" so it can be rotated.
- **Password reset can revoke keys:** `POST /api/v1/admin/users/{id}/password` takes `revoke_api_keys` (default false); the admin form has "Also revoke this person's API keys", unticked. It deletes every key the account owns and audits the count; if the keys cannot be revoked after the password was reset, the answer is 500 `keys_not_revoked` ("The password was reset, but the API keys were NOT revoked."). A person's own password change, a password reset by email ("Forgot password", #451), `server unlock` and `server reset-factors` never touch keys.
- **Audit:** every key issued or revoked, own or on someone's behalf, writes `api_key_issued` / `api_key_revoked` (entity type `api_key`) to the admin audit log; a revoke-on-reset writes `api_keys_revoked` on the account with the number of keys.
- **Lists and settings API:** `GET /api/v1/admin/api-keys` entries gain `created_by` and `beyond_limit`. `GET /me` gains `can_create_api_keys` and `api_key_max_lifetime_years`.
- **Existing keys:** an administrator's existing keys keep acting as administrator, unchanged.

### Staff Search Now Covers Every Ticket They Can See (#444)

A search in the ticket list used to cover only the selected queue, so staff searching from "Mine" never found a ticket assigned to someone else. **For staff and administrators the search box now covers every ticket the viewer can see, whatever queue is selected.**

- The results say "Searching all tickets you can see". A chip, "Only my tickets" (or "Only unassigned"), narrows the search to the queue; clicking it again widens it. With an empty box the queue is unchanged.
- **Who can see what is unchanged.** Administrators, and staff while `ticket_scope_enforced` is off, see every ticket. With it on, staff see only what their groups' scope admits, so a group-scoped staff member still cannot find another group's ticket.
- Requesters are unchanged and see only their own tickets.
- **API:** new `GET /api/v1/tickets?scope=visible` for staff and administrators. Older servers ignore an unknown scope and answer as `mine`. Existing scopes behave as before.
- No migration and no new index: the query is the one the administrator's All queue and the staff scope list already use.

### Forgot Password by Email (#451)

The sign-in page has a **Forgot password?** link when the server can send email. The person enters their address, is mailed a link, and chooses a new password.

- **Requires email.** With `SMTP_HOST` and `SMTP_FROM` unset the link is not shown, and the new endpoint answers `503 email_not_configured`. An administrator setting a password directly still works and is unchanged; it is the route when email is off.
- **Migration 000049** adds `password_reset_tokens`. Only a hash of each link is stored. It does not touch existing data and the down migration drops the table.
- **The link:** works **once**, for **30 minutes**, and a newer request replaces the older link. An account is mailed at most **once per five minutes**, however many requests arrive.
- **Every session of that account is ended** when the password is changed, so anyone holding an old session is signed out.
- **The link does not sign anyone in.** After choosing a password the person goes back to the sign-in page and signs in with the new password **and their second factor**, if they have one.
- **Not for everyone:** nothing is mailed to a disabled or deleted account, to an account that has no password (an SSO account that signs in only through OIDC or SAML, or a local account created without one; "Forgot password" does not give either a password), or, when SAML limits password sign-in to administrators (`saml_enabled`), to anyone who is not an administrator. The page gives the same confirmation either way, so it does not reveal which addresses have accounts.
- **Audit:** each reset writes a `password_reset_by_email` entry for the account, with no actor, next to the existing `password_reset_by_admin`.
- **New endpoints** (public): `POST /api/v1/auth/forgot-password` (always `202`, `429` when asked too often, `503` without email) and `POST /api/v1/auth/reset-password` (`204`, `400` for a password outside 8 to 72 bytes, `422 token_invalid` for a link that does not work). `GET /api/v1/auth/providers` gains `password_reset`. See `docs/api/openapi.yaml`.
- **Rate limits:** both endpoints are counted per client address against `AUTH_RATE_LIMIT_PER_MINUTE`.
- **Link host:** built from `BASE_URL`, never from the request. Make sure it is the address people reach the help desk at.

### OAuth Clients Now Act Through a Service User

Every OAuth client now has a **service user**: an account made with the client, named after it, that the client acts as. Upgrading creates one for each existing client (migration `000041`), named after the client, with the staff role and no groups.

What changes:

- **Clients can now write.** A client holding `tickets:write` creates tickets and replies as its service user, and the audit trail shows the client's name. Before, those calls were refused with `403 user_identity_required`. Nothing else about the scopes changed; a client with only `tickets:read` is still refused.
- **Service users are listed on Admin → Users**, with `service_account: true` and `auth_type: "service"` in the API. They can be renamed (which renames the client too), set to staff or user (never admin), given groups, and disabled; the server refuses the rest on `PATCH /admin/users/{id}` with `409 service_account` (`email`, a `role` of `admin`, and `reset_mfa: true`; none of the request is applied). This is stricter than the first 1.3.0 betas, which accepted a change of address. **Disabling the user stops the client**; enabling it restores it. A service user cannot be deleted on its own: `DELETE /admin/users/{id}` answers `409 service_account` and says to delete the client instead. Deleting the client soft-deletes its user; tickets it wrote keep its name.
- **They never sign in.** No password, no MFA, no passkey, no SSO, no admin role. The database enforces it, and the API answers `409 service_account` to an attempt.
- **They are hidden from assignee pickers and round-robin, do not count as administrators, and are never mailed.** A ticket a client opens sends no requester email.
- **`POST /admin/oauth-clients` returns `user_id`**; `GET /admin/oauth-clients` returns `user_id` and `groups` for each client.
- **With ticket scope enforcement off, clients behave as before.** Turning it on now tells you, on save, which clients have a service user in no group (they will see no tickets), and **Security warnings** (`oauth_clients_without_groups`) keeps listing them until each has a group or is disabled. Add the groups before you turn enforcement on.
- `GET /me` for a client keeps its shape, and a client still cannot reach the password, MFA and passkey routes.

If you downgrade, the `down` migration deletes the service users. Tickets they wrote stay, with no author link.

### Staff Email About Tickets (#441)

Staff are now emailed about their tickets, which 1.2 never did. **On an upgraded install, staff email starts switched off for everyone. Turn it on under Settings → Email when you're ready.** Migration `000052` stores all three administrator overrides as Off when it finds existing users; a fresh install is unchanged (all three switches on by default). Nothing is sent while email is not configured (`SMTP_HOST` and `SMTP_FROM`).

- **When:** a ticket is assigned to you, a ticket is assigned to a group you belong to, and a requester (an account holder or a guest) replies on a ticket assigned to you. A requester's reply on a group ticket that nobody personally holds goes to the group's members.
- **Not when:** you did it yourself, a colleague replies, or someone adds an internal note. A staff member who filed a ticket and replies on it counts as the requester. A reopen is mailed too: see *Reopening a Ticket (#464)* below.
- **Your choice:** three switches under **Your account → Email about tickets**: *Tickets assigned to me*, *Activity on tickets I'm assigned to or follow* and *My groups' tickets*. The middle switch was called *Activity on my tickets* before followers existed; it is the same setting (`email_notify_activity`) with a new label.
- **Administrators can force a switch on or off for everyone** under **Settings → Email** (`staff_email_assigned_override`, `staff_email_activity_override`, `staff_email_group_override`: `user`, `on` or `off`, `user` by default). On an upgraded install they start as **Off for everyone** (migration `000052`); an override an administrator had already stored is kept. Turn email on by setting them back to *Use each person's choice* or *On for everyone*.
- OAuth clients' service users are never emailed, even when they are members of a group.
- The emails carry only the ticket's tracking number and a link, like every other email from the help desk.
- Database migration `000042` adds three columns to `users`. It is safe to apply on a running instance.

### Ticket Followers (#441)

Staff and administrators can now **follow a ticket** (a Follow button on the ticket page, or `PUT` and `DELETE /api/v1/tickets/{id}/follow`) and are emailed about its activity: a ticket they follow being assigned, and its requester replying. This is new behaviour, not a change to anything that existed.

- **Followers are emailed under the *Activity on tickets I'm assigned to or follow* switch**, and the administrator's override of it (`staff_email_activity_override`) applies to them. Nobody is emailed about their own change, and nobody is emailed twice for one event whatever mix of assignee, group member and follower they are.
- **People only.** An API key, an OAuth client or a service user cannot follow or unfollow: `403 session_required`. Reading the list, `GET /api/v1/tickets/{id}/followers`, works for staff and administrators, and for a key with `tickets:read`.
- **Staff always see who follows a ticket.** There is no setting to hide the list.
- **Following is audited** (`followed`, `unfollowed`). Administrators see those entries on the ticket's Activity feed and in the admin-wide audit view. **Staff do not, until an administrator turns on the new setting `staff_can_view_follow_audit`** (Settings → General → Audit log: *Staff can view follow history*; off by default, session only). Requesters never see them.
- A follower who can no longer see the ticket (ticket scope was turned on, or their groups changed) is skipped when the mail is sent, and **keeps following**, so they are mailed again if they can see it again.
- **Reassigning a ticket ends the old assignee's follow.** If you followed a ticket you were assigned, and it is then assigned to someone else or to nobody (or your account is deleted, for tickets that are still open), your follow is removed and recorded as `unfollowed` with whoever moved the ticket as the actor. Click Follow again to keep the mail. Moving a ticket between groups does not touch anyone's follow.
- **The assignee is not mailed as a follower, and neither are the members of the assigned group when the group itself is mailed.** The group is mailed for an assignment, and for a requester's reply on a ticket nobody holds. For those, their own switches ("Tickets assigned to me", the group switch) decide, and following changes nothing. If somebody in the group holds the ticket, the group is not mailed for a reply, so a member who follows it is mailed as a follower.
- Database migration `000046` adds the `ticket_followers` table. It is safe to apply on a running instance. Migrations `000043` to `000045` belong to other changes in the same release. Upgrading from 1.2 straight to the final 1.3.0 applies all of them in number order. A database that ran a 1.3.0 beta build containing `000046` before those were merged is a different case: the migration tool only moves forward from the version the database is at, so it would skip `000043` to `000045`. Do not run such a beta database on the final release without checking that those migrations were applied.

### Webhooks: delivery history and Redeliver (#448)

Admin, Webhooks has a History button per hook: its last 25 deliveries with the outcome, status, attempts and time. No payloads and no error text are kept. Redeliver sends a delivery's event again as a new delivery. A Slack, Teams, Discord or Jira message is rebuilt from the ticket as it is now, so it can differ from the original; a reply's text is sent only if the reply still exists and is public. Raw hooks get the same ids-only body again.

This adds a database table through a migration that runs at start-up.

A retry that is dropped (the hook was disabled, paused because its API key, or that key's owner, is no longer active, or unsubscribed from the event, its ticket filter no longer admits the ticket, or the body cannot be built) now shows in History as **Dropped** with the reason. Before, its entry stayed on "Retrying" for ever. **Migration 000051** adds the `dropped` result and a `drop_reason` column to the history table; existing entries are unchanged, and the down migration turns dropped entries into `other`. The history API adds `result: "dropped"` and a `drop_reason` field, present only on dropped entries. Changing a hook's URL still clears the whole history, as before, so after a URL change you see an empty History rather than a dropped entry (`url_changed` appears only for an attempt that was in flight at that moment).

### Webhooks: a ticket filter (#413)

A hook can now be limited to tickets in chosen categories, priorities or assigned groups (Admin, Webhooks, Ticket filter), or inverted to send for every ticket that does not match. A hook with no filter, which is every hook after the upgrade, behaves exactly as before. The raw payload is not changed by a filter.

It works together with the history above and the retries under Changed behaviour, as follows. An event a hook's filter turns away is not a delivery for that hook: no `X-GHD-Delivery`, no retry, no History entry. The filter is read as it is now, not once. It is tested when an event is fanned out to the hooks, again at every attempt of a queued delivery, and again at Redeliver. If you tighten a filter, deliveries already waiting for a retry that it no longer admits are dropped at their next attempt, without a send or a History entry, the same way a retry is dropped when the hook is disabled, unsubscribed from the event or given a new URL. If you widen it, they still go out. A retry is tested against the ticket as it was when the event happened; a delivery queued before this upgrade has no ticket to test and is sent whatever the filter says. Redeliver tests the ticket as it is now, and is refused with `409 webhook_filter_excludes` when the filter does not admit it (if the hook has a filter and the ticket is gone, the filter cannot be checked and it is refused with `409 webhook_filter_unverifiable`; a hook with no filter is still sent). Nothing about this changes what a hook is sent: the raw payload and the chat messages are the same, and the ticket details used for the test are never put in a body or kept in the History.

Since #506, auto-assignment happens in the create's own transaction, so `ticket.created` carries the routed group and a group condition can match it.

This adds a column to `webhook_configs` through a migration that runs at start-up.

### New Environment Variables (Compose-Forwarded)

The following variables are now honoured when set in `docker/.env`. They are automatically forwarded to the app container and have sensible defaults in Docker Compose:

#### `AUTH_RATE_LIMIT_PER_MINUTE`

- **Default**: `10`
- **Description**: Failed password attempts per account per minute before a 429 response (sign-in and wrong current passwords on `PATCH /me/password`, each counted separately). `0` disables both password-attempt limiting and signup limiting. In-process counters reset on restart; each replica maintains its own budget, so N replicas multiply the limit by N.
- **Note**: TOTP verification has its own strict budget (durable on the user row) and is not affected by this setting.

#### `AUTH_THROTTLE_DELAY`

- **Default**: `1s`
- **Description**: How long a login, or a wrong-current-password guess on `PATCH /me/password`, waits before checking the password, for accounts that have spent their failed-attempt budget. Bounds online guessing to the server's bcrypt throughput (roughly 10–15 guesses/second/core). Must be a positive duration (e.g., `1s`, `500ms`, `1m`); cannot be zero at startup.
- **Impact**: A legitimate user who exhausts the budget still succeeds — their correct password is always honoured, just after the delay.

## Changed behaviour

### Reopening a Ticket (#464)

A reopen is any move from Resolved or Closed back to an open status: a requester's reply on a Resolved ticket (an account holder, or a guest), a status change by staff or an administrator (`PATCH /tickets/{id}`, or MCP `update_ticket_status`), and `POST /tickets/{id}/reopen`. Until now a reopen changed the status and nothing else. It now does five things, all in the same transaction as the status change:

- **The ticket is flagged.** `reopen_count` (new on the ticket JSON for staff and administrators only, omitted when 0, never reset) counts reopens, and staff and administrators see a **Reopened** badge on the ticket and in the queue while the ticket is open. It goes away when the ticket is resolved again. Tickets reopened before the upgrade start at 0.
- **The priority goes up one level on the first reopen only** (low, medium, high, critical; critical stays). This is a behaviour change on an existing action: a ticket that used to keep its priority through a reopen now gets a `priority_changed` entry and a new priority, and can match a different SLA policy. Later reopens do not raise it again.
- **The SLA starts over**, on every reopen (the priority rise is the first reopen only). The earlier record is kept, the ticket shows the **SLA restarted** badge (staff and administrators), and the audit log gets `sla_restarted`. Nothing happens to the SLA while SLA tracking is switched off. Before this, a reopened ticket kept its old record, which could already be met or breached.
- **Staff are emailed**: the assignee (under *Activity on tickets I'm assigned to or follow*), the assigned group's members (under *My groups' tickets*, only while nobody personally holds the ticket, as for a reply) and the ticket's followers (under the activity switch, whoever holds it), once each, and never the person who reopened it. A group member of a held ticket hears about a reopen only if they follow it. The administrator's overrides apply. On an upgraded install nobody receives these until an administrator turns staff email on under Settings → Email.
- **A status change out of Resolved or Closed is now audited as `reopened`** beside `status_changed`, and **raises the `ticket.reopened` webhook event**, which it did not before. A webhook subscribed to `ticket.reopened` will see it for those changes as well as for a requester's reply and `POST /reopen`.

- **A priority change can no longer undo a restart.** If a ticket's priority is moved to one with no SLA policy and later to one with a policy, the new SLA record resumes from the clock it left, or from the ticket's last reopen if that is later (it used to start from the ticket's creation, which could erase a restart). A reopen is remembered as a restart point even when it lands on a priority with no policy. The older record's met and breach marks are not copied. A ticket that never had an SLA record still starts from creation.


Database migration `000047` adds `tickets.reopen_count` (an integer, default 0), `last_reopened_at` and `paused_seconds_at_last_reopen`. It is safe to apply on a running instance.

### MCP `add_reply`: Public Replies Now Email the Requester

**Behaviour change.** Until now a public reply posted through the MCP `add_reply` tool was saved but never emailed to the requester, although DESIGN.md says it is and the REST reply does it. From 1.3.0 it is: the guest's address (with a guest link) or the reporting account's address, the same as a REST reply.

- Anything you have connected over MCP, including AI agents, can now send mail to your customers. Review which credentials have `tickets:write` before upgrading.
- Internal notes (`internal: true`) still send nothing.
- MCP has no `notify_customer` switch; a public reply always notifies. Use an internal note for anything that should not reach the requester.
- **REST change, staff and administrators only:** a staff member or administrator who replies publicly on a ticket they filed themselves is no longer emailed a copy of their own reply. Before, they were. This applies over REST and MCP. Webhook subscribers see it too: the reply event's `reporter_email` is empty in that case.
- **Unchanged for requesters:** an ordinary user replying on their own ticket still gets the email copy, as before. Replies on other people's tickets are unchanged.

### One Change, One Event: Webhooks and Staff Email (#506)

A change to a ticket is now **one event**, written to the notification queue in the same database transaction as the change. Before, each field changed was its own event, announced after the change had committed; a crash in between lost the notification. No migration, and no setting to change.

**Webhook receivers**

- **One delivery per change, per hook.** A hook subscribed to `*`, or to several names, used to get two or three deliveries for one click (a `PATCH` of status, assignee and priority; a reopen; a reply that reopens). It now gets one, with `X-GHD-Event` and `Type` set to the first name, in this order, that the hook subscribes to: `ticket.created`, `ticket.reopened`, `ticket.resolved`, `ticket.closed`, `ticket.status_changed`, `ticket.assigned`, `ticket.priority_changed`, `ticket.classified`, `ticket.replied`, `ticket.note_added`, `ticket.linked`. A hook subscribed to a single name is told that name, with the body it always had.
- **A receiver that switches on `Type` can miss a kind.** The body does not list the other kinds. A requester's reply that reopens a ticket arrives as `ticket.reopened` only, so a receiver that handles just `ticket.replied` will not see that reply. If you need more than the label, read the ticket through the API by `TicketID`.
- **The same status move has the same names from every door.** A move into Resolved is `ticket.status_changed` and `ticket.resolved`; into Closed, `ticket.status_changed` and `ticket.closed`; out of Resolved or Closed into an open status, `ticket.status_changed` and `ticket.reopened`. So `ticket.status_changed` now also fires for the Resolve button, the Close button, the auto-close and a requester's reopening reply, and `ticket.resolved` and `ticket.closed` now also fire for a `PATCH` of `status_id`.
- **New events:** `ticket.priority_changed` and `ticket.classified` (category, type or item). A hook subscribed to `*` starts receiving them. Neither body has a `Payload`; read the ticket by `TicketID`. They can be selected in Admin → Webhooks and are in the `WebhookEvent` enum.
- **A status change to the status the ticket already has announces nothing about status.** 1.2.x sent `ticket.status_changed` for it. That holds for a `PATCH` or an MCP `update_ticket_status` that repeats the current status, Resolved included: a `PATCH` that repeats Resolved and assigns someone sends `ticket.assigned` only, and the guest is not mailed or given a new link. Only the Resolve button on an already-Resolved ticket, with new notes, is still a resolution: it sends `ticket.resolved` only.
- **The body is unchanged**: ids only, as before. No tracking number, no kinds, no version flag.
- Chat and Jira messages include reply text only for a hook that subscribes to `ticket.replied` (or `*`), and Jira includes the ticket's status name only for a hook that subscribes to a status event (`ticket.status_changed`, `ticket.resolved`, `ticket.closed` or `ticket.reopened`); a hook on `ticket.reopened` alone gets its Reopened message without the reply, as in 1.2.x. Internal notes are never included.
- Chat and Jira formats add one line under the headline naming the change's other kinds ("Also: assigned, priority changed"), but only kinds that hook subscribes to; a hook subscribed to a single name gets no such line. Jira gains an optional `also` key under the same rule.
- Rows queued before the upgrade are delivered as they were.
- **Chat messages for `ticket.priority_changed` and `ticket.classified`** are headed "Priority changed" and "Reclassified". They name no value (no priority, category or type name).
- **A routed new ticket's `ticket.assigned` delivery names no actor** (`ActorID` is null), as in 1.2.x: routing is the system's act, not the creator's. The `ticket.created` delivery for the same ticket still carries the creator. A hook subscribed to both is told `ticket.created`, with the creator.

**Ticket behaviour**

- **`PATCH /tickets/{id}` is all or nothing.** The status, assignee, classification and priority are applied in one transaction. A refusal partway through now leaves the whole request unapplied; before, the steps ahead of it were already saved.
- **Auto-assignment happens in the create's transaction.** A new ticket is announced once, as created and assigned.
- **An assignment that changes nothing announces nothing (changed from 1.2.x).** Assigning a ticket to the user and group it already has, or clearing an already-unassigned ticket, used to send `ticket.assigned` and the assignee's email again. It now sends neither, as with a same-status or same-priority change. The audit entry for the request is still written, as it is for a same-status move.
- **Reclassifying announces `ticket.classified`**, and a priority change announces `ticket.priority_changed`. Reclassifying still writes no audit entry.

**Staff email**

- Who is mailed is decided by the server when it first picks the change up, a moment after the commit, not on the request. At most one mail goes to any address in one change (compared without case); when a guest's address is also a staff member's, the staff mail is the one sent. A person who qualifies for several reasons in one change gets one mail: the first of assigned, reopened, requester replied whose switch is on. Mail queued before the upgrade is sent as it is.
- **A failed lookup of a ticket's followers or of a group's members retries the whole change.** Before, the mail to those people was dropped without a retry, and only a failed read of staff accounts was retried. Nobody is mailed from a partial answer, so a retry does not mail anyone twice. A lookup that keeps failing holds back every mail for that change, the requester's included: the change is tried 8 times in all over about an hour, then the row is marked failed. The error in the log is "staff email: could not work out who to notify".
- **A slow split can no longer send the same mail twice.** If working out who to mail took longer than the 5-minute claim, a second worker could split the same change again after the first one's mail had gone out. The second now finds the change already taken and queues nothing.
- A guest may get a different template: the first of created, reopened, resolved, replied, status changed that the change carries. A `PATCH` to Resolved now says "We have resolved", and a status change that reopens says "We have reopened".

### Webhooks: failed deliveries are retried (#448)


A delivery that fails with a connection error, DNS or TLS failure, timeout, 5xx or 429 is tried again, 30 seconds doubling to an hour, 8 attempts in all. A `Retry-After` header from the receiver is honoured up to an hour. A blocked (internal) address and any other status, such as 400 or 404, are not retried. If you change a hook's URL, deliveries still waiting for the old address are dropped, and so are deliveries waiting for an event the hook no longer lists.

Every delivery now carries two headers: `X-GHD-Delivery`, a UUID that stays the same across the retries of one delivery (use it to drop a repeat), and `X-GHD-Event`, the event name. The signature headers are unchanged.

### Admin API: data left behind, half-applied edits and path checks (#419)

Scripts and API keys that call the admin API can see these changes. Nothing needs doing in the admin UI.

- **Deleting a category, type or item also deletes its custom-field assignments** (a category's include those of its types and items). They used to be left behind, pointing at a record that no longer existed. Assignments left over from deletes made on earlier versions are removed once by migration 000039 (those whose category, type or item no longer exists); it cannot be undone, and the rows it removes pointed at nothing.
- **Deleting a group now writes an `unassigned` audit entry for every ticket it held**, open or not, in the same transaction as the delete. Before, the tickets silently lost their group with no entry. The entry's before and after values name `assignee_group_id`, which the audit view now shows.
- **`PATCH /admin/users/{id}` checks every field before it writes any.** `{"disabled": true, "email": "bad"}` answers 400 and leaves the account enabled; it used to disable the account, end its sessions and then answer 400. The same goes for an email another account holds (409) and a change that would remove the last active administrator (400): both are refused before anything is written, where `{"reset_mfa": true, "role": "user"}` on the only administrator used to clear their factors and then answer 400.
- **Soft-deleted accounts answer 404** to `PATCH /admin/users/{id}` (for any body, `{}` included, which used to answer 200 with the deleted record) and to `POST /admin/users/{id}/password` (which used to set the deleted account's password and answer 204).
- **Same order of checks for a session and an API key** on the password reset and on PATCH: the body is checked first (400), then the caller (403), then the account (404). Before, an API key aimed at an administrator or a deleted account got 403 or 404 before its body was looked at.
- **Mismatched ids in a URL answer 404.** `/categories/{id}/types/{typeId}` (PATCH, DELETE), the item routes under it, and all nine custom-field assignment routes now require every id to exist and to belong to the one before it, and an assignment to belong to the scope in the path. Before, only the deepest id was read, so a wrong parent id was ignored. Creating an assignment for a category, type or item that does not exist answers 404 (it used to store an assignment for nothing), and listing the items of an unknown type, or the assignments of an unknown category, type or item, answers 404 (it used to answer an empty list).

### Answers that now say something different (#421)

The rest of the #421 changes to scripts and integrations change what an answer says.

- **A NUL character in free text is `400`, not `500`.** A NUL (or invalid UTF-8) in a ticket's `subject`, `description`, `guest_name` or `guest_phone`, or in a reply `body`, reached Postgres, which refuses it: a `500`, and for a create a tracking number already taken, so a gap in the numbering. It is now `400 bad_request` naming the field, before anything is written. Nothing else changed: every other character (tabs, newlines, escape codes) is accepted and stored as given, as before.
- **`GET /api/v1/auth/providers`** is served as `application/json` (it had no content type). `oidc` and `saml` now mean "loaded and working", not "switched on": an identity provider that did not answer shows `false`, and SAML shows `true` whenever it is loaded, whatever `saml_enabled` says. `local` is still always `true`; the new `local_admin_only` is `true` when `saml_enabled` limits password sign-in to administrators.
- **`GET /api/v1/admin/oidc` and `/admin/saml`** gain `live`. `configured: true, live: false` means "saved, not working". `configured` is unchanged.
- **`GET /api/v1/auth/signup/status`**: `open_registration` is now what is in force, so it reads `false` when an allowed-domains list limits signup, even with the open-registration setting on. The new `restricted_by_domain_list` says why. Who can sign up is unchanged.
- **`Retry-After` on a `429`** from the sixty-second login, signup and guest limits is now the seconds left in the window (1 to 60) instead of always 60. The 15-minute MFA lock still says 900.
- **A server crash inside a request** answers the JSON `500 internal_error` body instead of an empty body.
- **Passkey sign-in and registration (`.../passkey/finish`, `.../register/finish`)** answer `500` when the server cannot write the session (they said `400 no_challenge`), and refuse a body over 64 KiB with `413 too_large`.
- **`POST /api/v1/me/mfa/enroll/confirm`** answers `500` when the server cannot save the factor (it said `400 invalid_code`). A wrong code and an enrolment that was never started are still `400 invalid_code`.
- **`POST /api/v1/guest/resend`** accepts an empty body (it was `400`). Malformed JSON is still `400`.
- **`DELETE /api/v1/tickets`** is `405` as before. Its `Allow` header carries `GET` and `POST` as two header lines, which is what the server always sent; clients that read only the first line saw `GET`.

### Email addresses must be ASCII

New accounts, sign-ins that create or link an account, and password resets refuse an address containing non-ASCII characters. Signup, administrator create and edit, a typed `reporter_email` and guest submission answer `400`; a first-time SAML or OIDC sign-in redirects to `/login?error=invalid_email` (or `domain_not_allowed` when an allowed-domains list is set). Go and Postgres lower-casing fold some non-ASCII letters into ASCII ones, so such an address cannot be compared reliably.

- **Existing accounts** keep signing in by their current identity provider subject. They cannot be newly linked by email, and a sign-in that carries a non-ASCII address keeps the stored one.
- **What to check:** `SELECT count(*) FROM users WHERE email ~ '[^\x01-\x7f]'`. Zero means nothing changes for you.

### Admin API: additive and corrected answers (#419, response half)


- **`auth_type` reports OIDC.** New values: `oidc`, `local+oidc`, `saml+oidc` and `local+saml+oidc`. `local`, `saml` and `both` (password plus SAML) keep their meaning for accounts without OIDC. Accounts that change value: an account that signs in only through OIDC, or has a password and OIDC, used to read `local`; an account with SAML and OIDC used to read `saml`; and one with a password, SAML and OIDC used to read `both`. A client that switches on `auth_type` with a fixed list of values needs to handle the new ones.
- **`visible_on_new` on a new field assignment:** the API default is still false when omitted. The database column default (it was true) is now false too (migration 000040); existing rows are untouched.
- **`GET /admin/sla/policies` is ordered by severity** (critical, high, medium, low, then policies with no priority), then by name. It was ordered by the priority text, which put `low` before `medium`.
- **`POST /admin/oauth-clients` returns `id`** (the record id that `DELETE` takes), beside `client_id` and `client_secret`.
- **`PATCH /admin/users/{id}` returns the real `passkey_count`**; it always said 0.
- **`PATCH /admin/webhooks/{id}` returns the saved row.** After a URL change `last_delivery` is `null`; it showed the old receiver's result.
- **`PATCH /admin/custom-fields/{id}` returns the saved, trimmed row.** `POST` and `PATCH /admin/statuses` return the status's real `ticket_count`; they said 0.
- **A blank webhook `secret` on `PATCH` still clears it**, by design and for administrators only; now documented in the API spec and DESIGN.md.

### Ticket and Settings API Responses (#420)

Changes a script or integration may notice:

- `POST /tickets` now answers with the ticket after routing, so `assignee_user_id` / `assignee_group_id` are filled in when a routing rule or the auto-assign setting applied. Before, they were absent until the ticket was fetched again.
- `POST /tickets/{id}/replies` now includes `author_name` in the reply, as the reply list does.
- `PATCH /tickets/{id}` with no body at all is now the same as `{}`: nothing changes, and the answer is `200` with the ticket. Before, it was `400`. Malformed JSON is still `400`.
- `PATCH /admin/settings` now refuses JSON `null` (`400`) for `attachment_scan_address`, `attachment_allowed_types` and the four `attachment_reputation_*_enabled` toggles. Before, `null` was stored and read as "unset" or "off". To clear the scanner address, send an empty string. To restore the default allowed types, send the list you want.
- The six choice settings in `PATCH /admin/settings` (`closed_reopen_policy`, `audit_mask_requester_names`, `attachment_scan_policy`, `attachment_infected_handling`, `attachment_mismatch_handling`, `attachment_reputation_refresh`) now answer their own error code for every refusal: `invalid_closed_reopen_policy`, `invalid_audit_mask_requester_names`, `invalid_scan_policy`, `invalid_infected_handling`, `invalid_mismatch_handling`, `invalid_reputation_refresh`. Before, `null` and a value of the wrong type (a number, a boolean) answered `bad_request` for most of them. The status is still `400`, and the messages are unchanged. A script that matched on `bad_request` for these needs to match the specific code instead.
- Every route under `/tickets/{id}` now accepts a tracking number (for example `GHD-2026-000001`) as well as a UUID. Before, only `GET /tickets/{id}` did; the others answered `400` with `bad_request` or `invalid_id` ("invalid ticket ID"). Those two `400`s no longer exist. A UUID works exactly as before.

### Antivirus Scanning: Banner for Offline Policies

Administrators see a banner on the admin panel with the exact fix when any attachment exists, no scan policy was ever saved, and no scanner address is set:

1. Add `COMPOSE_PROFILES=antivirus` and `CLAMAV_ADDR=tcp://clamav:3310` to `docker/.env`
2. Run `docker compose up -d`
3. Confirm the scanner address in Settings → Attachments

The banner reads:

> Attachments are not being scanned. To turn scanning back on, add `COMPOSE_PROFILES=antivirus` and `CLAMAV_ADDR=tcp://clamav:3310` to `docker/.env`, run `docker compose up -d`, then confirm the scanner address under Settings → Attachments. To keep scanning off, choose "Do not scan" there.

The scanner address counts as set if it is in Settings or in the environment. Settings → Attachments also shows the scanner's policy, whether an address is configured, whether it answers, and what that means for uploads.

Explicitly choosing "Do not scan" in the policy dropdown clears the banner. The exception is documented in DESIGN.md ("The one exception (#459)", in the attachment scanning section) — until a policy is explicitly set, the banner warns about unscanned uploads even in "off" mode.

### `APP_ENV` Variable (Removed from Documentation)

The `APP_ENV` environment variable no longer has any effect. Setting it is harmless and will not break anything, but it is no longer documented. If you set it in your `.env`, you can safely ignore it or remove it; the server logs a single info message at startup whenever it is set, whatever its value. The log level is controlled by `LOG_LEVEL`.

### Attachment Storage Check (`ATTACHMENT_DIR`)

At startup the server checks that `ATTACHMENT_DIR` (default `/data/attachments`, fixed to the `attachments` volume under Compose) exists and can be written to. If it cannot:

- the server logs a warning and administrators see a banner, "Attachment uploads are disabled", with the reason;
- every upload (staff, requester and guest) is refused with `503 attachments_unavailable` until the directory is fixed. Everything else runs, and attachments already stored can still be downloaded while the directory is readable;
- the check repeats on each upload, so fixing the permissions needs no restart.

Before this, an unwritable directory showed up as a failed upload partway through.

### Tickets Left Assigned to a Deleted User Go Back to the Queue (#524)

In 1.2.x, deleting a user left their open tickets pointing at them. Such a ticket showed as "Unassigned" on its page, was not in the unassigned queue, and was in nobody's "assigned to me". 1.3.0's delete returns the user's open tickets to the queue (and writes an `unassigned` audit entry for each); **migration `000053` does the same, once, for the tickets a 1.2.x delete had already stranded.**

- **Which tickets:** open ones (neither Resolved nor Closed) whose assignee is a deleted user. Resolved and Closed tickets are history and keep the person who handled them. A **disabled** user is still in the user list, so their tickets are left alone.
- **What you see afterwards:** those tickets are in the unassigned queue, and each has an `unassigned` entry on its Activity feed and in the admin audit view, naming the deleted user it had. The entry has **no actor**, because no person ran the migration. Their `updated_at` is set to the time of the upgrade.
- **Nothing else changes.** No mail is sent, no webhook or event is raised, and no SLA clock moves. A group assignment needs nothing: deleting a group has always cleared it.
- **Rolling back:** the `down` migration does nothing. The tickets stay unassigned; the audit entries record who they had.

### Replies and Internal Notes Appear in Activity and the Audit Log

Replies and internal notes now appear in a ticket's Activity panel as "Erik Test replied publicly" or "Erik Test added an internal note", with the time and no text, and in the admin audit view as `reply_added` and `note_added`. **From the upgrade on:** replies and notes written before it are not backfilled and do not appear.

- **No words are recorded.** The entry holds the author, the time, the reply's id and whether it was internal. The text stays in the conversation, under its existing visibility rules.
- **Who sees it:** the same people who see Activity today, staff and administrators. A requester still gets 403 on the feed. A requester's or guest's reply shows as "Requester (name hidden)" or "A guest" under the existing `audit_mask_requester_names` setting. Staff see the entries in the admin audit view only for tickets in their scope.
- **Hide them if they crowd the log.** The admin audit page has a **Hide replies and notes** checkbox (API: `exclude_action`, repeatable).
- **Webhooks, MCP and email are unchanged.** The audit actions are separate from the `ticket.replied` and `ticket.note_added` webhook events.
- **No migration.** Replies and notes are capped on their own in the feed (the newest 500), so a long conversation cannot push the ticket's other history off it.

### The Server Logs Which Migrations Ran (#525)

The server now says what it did to the schema, once, at startup, before it opens the port:

- `database migrated from 21 to 55`: an upgrade from 1.2.0 (the first number is the version the database was at, the second the version it is at now). A new database says `from 0`.
- `database schema at 55, no migrations needed`: an ordinary restart.

The line carries `from` and `to` fields as well. Use it to check that an upgrade ran before you look anywhere else. Migrations still run on their own, before the server accepts traffic, and a failed one still stops the start with `running migrations: ...`.

## Upgrade steps

1. **Take a database backup.** If the server generates its JWT signing key (`JWT_SECRET` unset, shorter than 32 bytes, or an example value), the key is stored in the database, so every backup contains it: protect backups accordingly.
2. **Check your scripts and receivers against Breaking changes.** In particular: anything that uses an API key to create keys or OAuth clients, write users or groups, set passwords or change emails; any key-creating script that sends no `expires_at`; any raw-JSON webhook receiver that reads more than ids; any SMTP setup that sends a password without TLS or sets `SMTP_USER` against a server with no AUTH; any script that files or reclassifies tickets into an archived category, type or item.
3. **Staff email:** on an upgraded install, staff email starts switched off for everyone (migration 000052); turn it on under Settings → Email when ready.
4. **Restart on 1.3.0** (`docker compose up -d` from the `docker/` directory). Migrations run automatically at start-up, in number order.
5. **After the first start:** OAuth clients fetch a new access token once if the JWT key was replaced; give each OAuth client a group before turning ticket scope enforcement on; tell people who rarely sign in that passwords on an old hash must be reset within 30 days of the first start of 1.3.0 (administrators are exempt).

### Migrations

1.2.x ends at migration 000021. Going straight to 1.3.0 applies every later migration in number order, and **there is no migration 000048.** The number was retired while 1.3.0 was being built, so that no pre-release database could skip a migration. Nothing is missing: your database goes from 000047 straight to 000049. (#450 first planned 000048, but #451 merged with 000049 first, and the migration runner never applies a version below the one a database is already at.)

The ones these notes describe: 000038 (webhook ticket filter column), 000039 (removes orphaned custom-field assignments, once, irreversibly), 000040 (`visible_on_new` column default becomes false; existing rows untouched), 000041 (OAuth client service users), 000042 (three staff email columns on `users`), 000043 to 000045 (SLA rematch, the password-rehash clock, `users.must_change_password`), 000046 (`ticket_followers`), 000047 (`tickets.reopen_count`, `last_reopened_at`, `paused_seconds_at_last_reopen`), 000049 (`password_reset_tokens`), 000050 (`api_keys.created_by`, `webhook_configs.api_key_id`; the new #450 settings are rows in the settings table and need no migration), 000051 (the `dropped` webhook result and `drop_reason`), 000052 (stores the three staff email administrator overrides as Off on an install that already has users), 000053 (open tickets still assigned to deleted users go back to the queue; see *Tickets Left Assigned to a Deleted User Go Back to the Queue (#524)*) and 000055 (the text format columns; existing text is marked plain; see *Markdown in Ticket Text (#443)*). Those that say so are safe to apply on a running instance.

A database that ran a 1.3.0 beta build containing `000046` before `000043` to `000045` were merged would skip them: see *Ticket Followers (#441)*.

If you downgrade, the `down` migration of 000041 deletes the OAuth service users; see *OAuth Clients Now Act Through a Service User*.

## Rolling Back to 1.2.0

Migrations are forward-only as far as the server is concerned: it never runs one backwards, and there is no command for it. **A rollback is possible by hand. It discards everything that only 1.3.0 has, and it removes 1.3.0's security fixes.** Take it only if you must. This procedure was run end to end: a database upgraded from 1.2.0 with 1.3.0 activity on it was taken back to version 21, its schema matched a 1.2.0 database (same tables, columns, indexes and constraints; the one difference is below), and the 1.2.0 server then started on it and signed people in, served tickets, attachments, API keys and OAuth tokens, and kept the replies and tickets written under 1.3.0.

### Security: what you give up

1.2.0 does not have these 1.3.0 fixes, and a rollback runs without them. Weigh that before you start, and put the instance behind your own network controls until you move forward again:

- **Postgres is published on every network interface.** 1.2.0's Compose file publishes the database as `5432:5432` with the password `helpdesk`. Docker handles a published port before the host firewall sees it, so closing the port in `ufw` or `firewalld` does not protect it. 1.3.0 binds it to `127.0.0.1`. **The procedure below edits 1.2.0's Compose file to bind it to loopback and checks the result before it starts anything; do not skip that step.**
- Attachments: the download route refusing a request that is going to be rendered (`Sec-Fetch-Dest`), which is what stops an uploaded file being shown on the help desk's own origin.
- **API keys:** the restrictions in *Staff Can Create Their Own API Keys* (a key can create keys and OAuth clients again, write users and group membership, and change the settings that decide who sees tickets), the expiry every new key needed, and the fix that stopped a key deleting another administrator's key.
- SAML: hand-over cookies that can only be used once.
- MCP: the `Origin` check, the 1 MiB request cap and the binding of an SSE session to the credential that opened it.
- Others listed in these notes, among them the bcrypt cost, the 72-byte password limit, the 5-minute limit on a half-signed-in session, and the `404` for a ticket you may not see.

### What you lose

Each `down` migration undoes its `up`. These are dropped or deleted, and cannot be brought back except from the backup you take first:

- **Passkeys** (`webauthn_credentials`, 000029) and **guest links** (`guest_access_tokens`, 000022). People re-register their passkeys; a guest needs a new link.
- **The notification queue** (`notification_outbox`, 000031): any mail or webhook delivery still waiting is gone.
- **Webhook delivery history** (000037), each hook's **payload format** (Slack, Teams, Discord, Jira, 000026), its **ticket filter** (000038) and its last-delivery status (000034). Hooks go back to plain JSON.
- **Attachment scan results** (hash, detected type, virus name, quarantine flags, 000023) and the **reputation cache** (000024).
- **SLA**: pause bookkeeping (000025), the frozen elapsed times (000027), and every superseded SLA record (000043), the history a reopen kept.
- **Reopen counts and restart points** (000047), **followers** (000046), **password reset links** (000049), the 30-day password clock (000044), the must-change-password flag (000045), **staff email preferences** (000042) and **API key issuer and webhook pause links** (000050).
- **OAuth service users** (000041): the users made for each client are deleted, and the database removes what hangs off them by cascade: their group memberships (and any sessions or API keys they held, though a service user is not meant to have any). The clients keep working without a user, as in 1.2.0. Tickets and replies they wrote stay, with no author link, and a ticket assigned to one loses its assignee.
- **Text formats** (000055): `tickets.description_format` and `ticket_replies.body_format` are dropped. Description and reply text stays, but which posts were written as Markdown is forgotten, so Markdown posts written under 1.3 show as raw source, and 1.2.0 shows everything as plain text.
- **The generated JWT signing key** (`instance_secrets`, 000036). 1.2.0 needs its own `JWT_SECRET` in `docker/.env`: set a random secret of at least 32 bytes (for example `openssl rand -base64 48`). 1.2.0 only warns about a weak or example secret and signs with whatever you give it, and 1.3.0 discarded published example values as well as short and unset ones, so a value that 1.3.0 ignored is not safe to reuse. Every OAuth client fetches a new token either way.
- Pending signups that 1.3.0 created without a name or password (000032, 000033).
- **Which attachments a guest uploaded** (000054): `attachments.uploaded_by_guest` is dropped. The files stay; the record of who attached them is forgotten.

**Not undone** (they repair or seed data and have no inverse; their `down` does nothing): `000028`, `000039`, `000052` and `000053`. The three staff-email overrides `000052` stored stay in the settings table, and the tickets `000053` returned to the queue stay unassigned. 1.2.0 never reads those settings. Settings rows 1.3.0 wrote stay in the table. Passwords hashed at the new cost still work on 1.2.0.

**One difference from a 1.2.0 database:** `ticket_replies.guest_token` is the last column of the table instead of the fourth. Nothing reads columns by position.

### Procedure

You need a checkout of the **1.3.0** release, because the `down` files live in it (`backend/internal/database/migrations`) and 1.3.0 is deployed from an image, so a checkout may not exist or may be at another version. Check out the release first, from the directory your Compose stack was started in:

```sh
cd go-help-desk
git fetch --tags
git checkout v1.3.0
cd docker
```

1. **Stop the app and take a backup.** Do not skip this: the steps below delete data.

   ```sh
   docker compose stop app
   docker compose exec -T db pg_dump -U helpdesk helpdesk > ../backup-before-rollback.sql
   ```

2. **Run the down migrations**, newest first, each in its own transaction, starting from the version the database is at. Save this as `rollback.sh` in `go-help-desk/docker` and run `bash rollback.sh`:

   ```sh
   set -euo pipefail
   MIG=../backend/internal/database/migrations
   PSQL="docker compose exec -T db psql -U helpdesk -d helpdesk -v ON_ERROR_STOP=1"
   current=$($PSQL -Atc "SELECT version FROM schema_migrations")
   case "$current" in ''|*[!0-9]*) echo "database version '$current' is not a number: stop"; exit 1;; esac
   top=$(ls $MIG/*.down.sql | sort | tail -1 | xargs basename | cut -c1-6)
   if [ "$current" -gt $((10#$top)) ]; then
     echo "database is at $current but this checkout stops at $top: check out the release you run (v1.3.0)"; exit 1
   fi
   dirty=$($PSQL -Atc "SELECT dirty FROM schema_migrations")
   if [ "$dirty" != f ]; then
     echo "the database is marked dirty: a migration failed part-way; restore the backup, do not continue"; exit 1
   fi
   echo "database is at version $current"
   for f in $(ls $MIG/*.down.sql | sort -r); do
     name=$(basename "$f")
     v=$((10#${name:0:6}))
     if [ "$v" -gt "$current" ]; then continue; fi
     if [ "$v" -le 21 ]; then break; fi
     echo "rolling back $name"
     $PSQL --single-transaction < "$f" || { echo "STOPPED at $name: restore the backup"; exit 1; }
   done
   $PSQL -c "UPDATE schema_migrations SET version = 21, dirty = false"
   ```

   There is no `000048`; the script skips it because there is no file. If it stops, the failed file made no change (it ran in a transaction) but the ones before it did: restore `backup-before-rollback.sql` rather than continue.

3. **Check out 1.2.0 and bind Postgres to loopback, before anything starts.** 1.2.0's Compose file would publish the database on every interface (see *Security* above), so edit it first, then check it. Save this as `bind.sh` in `go-help-desk/docker` and run it with `bash bind.sh`, from the directory you ran `rollback.sh` in:

   ```sh
   set -euo pipefail
   cd ..
   git checkout v1.2.0
   cd docker
   sed -i.bak 's/"5432:5432"/"127.0.0.1:5432:5432"/' docker-compose.yml
   rm -f docker-compose.yml.bak
   cfg=$(docker compose config)
   if [ "$(printf '%s\n' "$cfg" | grep -c 'target: 5432')" != 1 ] \
      || ! printf '%s\n' "$cfg" | grep -B1 'target: 5432' | grep -q 'host_ip: 127.0.0.1'; then
     echo "Postgres is not bound to 127.0.0.1 only: do NOT start the stack"; exit 1
   fi
   echo "ok: Postgres is bound to 127.0.0.1 only"
   ```

   `git checkout v1.2.0` needs a clean working tree, so it also stops you if you have changed tracked files. If the check fails, stop and fix `docker-compose.yml` by hand until `docker compose config` shows `host_ip: 127.0.0.1` on the `5432` binding and no other. Do not use a `docker-compose.override.yml` for this: Compose before 2.24.4 ignores the `!override` tag without a warning and publishes the port on both.

4. **Start 1.2.0**, only once step 3 has printed `ok`. It builds the app from source, the way 1.2.0 did:

   ```sh
   docker compose up -d --build
   ```

   Keep your `docker/.env`, and make sure it has a `JWT_SECRET` as described above: 1.2.0's Compose file refuses to start without one.

5. **Check it.** Sign in as an administrator, open a few tickets, and download an attachment. `SELECT version, dirty FROM schema_migrations` should say `21 | f`. `docker compose port db 5432` should say `127.0.0.1:5432`.

To go forward again, run `git checkout -- docker-compose.yml` in `go-help-desk/docker` to drop the edit from step 3, then `git checkout v1.3.0` (it refuses while the file is changed) and start it: it runs every migration from 22, and says so in the log (`database migrated from 21 to 55`).
