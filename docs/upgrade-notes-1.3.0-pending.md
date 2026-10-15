# Upgrade Notes for v1.3.0 (Pending)

These notes document configuration and environment changes in v1.3.0. They will be folded into the release notes by PR #438.

## Breaking: Staff and Administrators Can No Longer File Into an Archived Category, Type or Item (#421)

Previously the server let staff and administrators create tickets under, and reclassify tickets into, categories and types an administrator had archived, and never checked items for anybody. That is reversed: **nobody, staff and administrators included, can newly pick an archived category, type or item.**

- `POST /api/v1/tickets`, `PATCH /api/v1/tickets/{id}` (with `category_id`/`type_id`/`item_id`) and the MCP `create_ticket` tool answer **400** (`bad_request`, for example `type_id is not an active type`) when the classification includes an archived node. This applies to sessions and API keys alike.
- A ticket **already** in an archived node keeps it. Unrelated edits, a PATCH that repeats the existing classification, and staff follow-ups of a closed ticket still work.
- Staff and administrators still see archived rows, now greyed out, labelled "(archived)" and not selectable in the new-ticket and reclassify pickers. A ticket's own current archived node stays selected so it can keep it.
- A PATCH that carries a refused classification now changes nothing: the status and assignee in the same request are no longer applied before the 400.

What to check: scripts or integrations that file or reclassify tickets as staff or admin into an archived node will start getting 400. Unarchive the node (Admin → Categories) or file into an active one.

## New: `unlock <email>` on the Server Console (#497)

`docker compose exec app /app/server unlock <email>` is the way back for a locked-out account, and it works when the web path cannot: a lost authenticator or passkey, a disabled account (the sole administrator included), a spent MFA lockout, or a federated administrator with no usable password. In one transaction it clears every second factor, re-enables the account, clears the MFA lockout, sets a new random password and prints it once, and ends all sessions. The password must be changed at the next sign-in.

- New migration `000045_must_change_password` adds `users.must_change_password` (default false). Nothing changes for existing accounts.
- `POST /api/v1/auth/local/login` gains `password_change_required` and `User` gains `must_change_password`. A session in that state is answered `403 password_change_required` everywhere except `PATCH /api/v1/me/password`.
- `reset-factors` is unchanged.

## An IP or Single-Label `BASE_URL` Disables Passkeys (#494)

Passkeys are bound to a domain name, so they work only when the host in `BASE_URL` is a domain name (`help.example.com`) or `localhost`. **If `BASE_URL` is an IP address (`http://192.168.1.10:8080`, `http://[::1]:8080`), a single-label host (`http://helpdesk:8080`) or contains non-ASCII characters, passkeys are disabled.** The server still starts. It logs `passkeys unavailable` once at startup, and administrators see a warning banner explaining that `BASE_URL` must be a domain name.

- `GET /api/v1/me/passkeys`, `POST /api/v1/me/passkeys/register/*` and `POST /api/v1/auth/local/passkey/*` answer `503 passkeys_unavailable`.
- `GET /api/v1/auth/providers` gains `passkeys` (`false` here) and `GET /api/v1/admin/security-warnings` gains `passkeys: {available, reason}`.
- An account whose only second factor is a passkey **cannot sign in** while passkeys are off: sign-in answers `403 passkey_unavailable` and tells the person to ask an administrator to reset their sign-in factors. An administrator's Reset MFA clears the passkey, after which the person signs in and enrols an authenticator app (TOTP). Accounts with TOTP are unaffected. This includes the last administrator: run `docker compose exec app /app/server reset-factors <email>` (from the `docker/` directory) on the server to clear their factors. Passkeys registered under a domain `BASE_URL` that is later fixed work again if the domain matches.

What to check: if your instance is on an IP or single-word host and you want passkeys, give it a DNS name and set `BASE_URL` to it. Changing the domain invalidates passkeys registered under the old one.

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

## MCP: Requests From Another Origin Are Refused (#460)

Every MCP route (`/mcp`, `/mcp/sse`, `/mcp/message` and anything else under `/mcp/`) now answers `403 origin_not_allowed` when the request carries an `Origin` header that is not your `BASE_URL`'s origin, before authentication is looked at. Requests with no `Origin` header, which is what SDKs, editors and `curl` send, are unaffected.

- A browser-based MCP client served from a different origin, or a reverse proxy that injects an `Origin` header, is now refused. Serve the client from `BASE_URL`'s origin or stop the proxy adding the header.
- 1.2.x accepted these requests on the SSE routes. This closes a path by which a web page on a sibling site could act through a signed-in user's session cookie. Upgrade if you expose MCP over SSE.
- A `BASE_URL` with a non-ASCII host stops the server from starting (passkeys refuse it); write it in punycode.

## JWT Secret (`JWT_SECRET`)

`JWT_SECRET` is now **optional**:

- If **unset or under 32 bytes**: A random 32-byte key is generated once and stored in the database. All instances in the cluster read the same key from the database.
- If **32+ bytes**: The environment value is used as-is (the long value wins over the generated key).
- If it is **exactly one of the example secrets published in this repository** (for example `change-me-in-production-this-is-really-insecure`): it is discarded, a generated key is used, and the server logs one warning. Anyone can read the example, so it must never sign anything. (`SESSION_SECRET` is different: an example value there is still flagged in the admin panel, not discarded.)
- **Upgrade path**: If you set `JWT_SECRET` to a long value in `.env`, it remains in use and nothing changes. If you unset it or provide a short value, a new key is generated on first boot. **OAuth clients must fetch a new access token once** (sessions are unaffected; they use `SESSION_SECRET`).
- **Backup implications**: Database backups now contain the JWT signing key. Restoring a backup onto another instance carries the key with it (tokens remain valid there).
- **Rotation**: To rotate the key, delete the row from `instance_secrets` and restart. All OAuth tokens issued with the old key become invalid.

## OAuth Clients Now Act Through a Service User

Every OAuth client now has a **service user**: an account made with the client, named after it, that the client acts as. Upgrading creates one for each existing client (migration `000041`), named after the client, with the staff role and no groups.

What changes:

- **Clients can now write.** A client holding `tickets:write` creates tickets and replies as its service user, and the audit trail shows the client's name. Before, those calls were refused with `403 user_identity_required`. Nothing else about the scopes changed; a client with only `tickets:read` is still refused.
- **Service users are listed on Admin → Users**, with `service_account: true` and `auth_type: "service"` in the API. They can be renamed (which renames the client too), set to staff or user (never admin), given groups, and disabled. **Disabling the user stops the client**; enabling it restores it. A service user cannot be deleted on its own: `DELETE /admin/users/{id}` answers `409 service_account` and says to delete the client instead. Deleting the client soft-deletes its user; tickets it wrote keep its name.
- **They never sign in.** No password, no MFA, no passkey, no SSO, no admin role. The database enforces it, and the API answers `409 service_account` to an attempt.
- **They are hidden from assignee pickers and round-robin, do not count as administrators, and are never mailed.** A ticket a client opens sends no requester email.
- **`POST /admin/oauth-clients` returns `user_id`**; `GET /admin/oauth-clients` returns `user_id` and `groups` for each client.
- **With ticket scope enforcement off, clients behave as before.** Turning it on now tells you, on save, which clients have a service user in no group (they will see no tickets), and **Security warnings** (`oauth_clients_without_groups`) keeps listing them until each has a group or is disabled. Add the groups before you turn enforcement on.
- `GET /me` for a client keeps its shape, and a client still cannot reach the password, MFA and passkey routes.

If you downgrade, the `down` migration deletes the service users. Tickets they wrote stay, with no author link.

## `APP_ENV` Variable (Removed from Documentation)

The `APP_ENV` environment variable no longer has any effect. Setting it is harmless and will not break anything, but it is no longer documented. If you set it in your `.env`, you can safely ignore it or remove it; the server logs a single info message at startup whenever it is set, whatever its value. The log level is controlled by `LOG_LEVEL`.

## Attachment Storage Check (`ATTACHMENT_DIR`)

At startup the server checks that `ATTACHMENT_DIR` (default `/data/attachments`, fixed to the `attachments` volume under Compose) exists and can be written to. If it cannot:

- the server logs a warning and administrators see a banner, "Attachment uploads are disabled", with the reason;
- every upload (staff, requester and guest) is refused with `503 attachments_unavailable` until the directory is fixed. Everything else runs, and attachments already stored can still be downloaded while the directory is readable;
- the check repeats on each upload, so fixing the permissions needs no restart.

Before this, an unwritable directory showed up as a failed upload partway through.

## Staff Email About Tickets (#441)

Staff are now emailed about their tickets, which 1.2 never did. **Once email is configured (`SMTP_HOST` and `SMTP_FROM`), every existing staff member and administrator starts receiving it, because all three switches are on by default.** Nothing is sent while email is not configured.

- **When:** a ticket is assigned to you, a ticket is assigned to a group you belong to, and a requester (an account holder or a guest) replies on a ticket assigned to you. A requester's reply on a group ticket that nobody personally holds goes to the group's members.
- **Not when:** you did it yourself, a colleague replies, or someone adds an internal note. A staff member who filed a ticket and replies on it counts as the requester. A reopen is mailed too: see *Reopening a Ticket (#464)* below.
- **Your choice:** three switches under **Your account → Email about tickets**: *Tickets assigned to me*, *Activity on tickets I'm assigned to or follow* and *My groups' tickets*. The middle switch was called *Activity on my tickets* before followers existed; it is the same setting (`email_notify_activity`) with a new label.
- **Administrators can force a switch on or off for everyone** under **Settings → Email** (`staff_email_assigned_override`, `staff_email_activity_override`, `staff_email_group_override`: `user`, `on` or `off`, `user` by default). To keep your staff's inboxes as they were, set all three to **Off for everyone** before turning email on.
- OAuth clients' service users are never emailed, even when they are members of a group.
- The emails carry only the ticket's tracking number and a link, like every other email from the help desk.
- Database migration `000042` adds three columns to `users`. It is safe to apply on a running instance.

## Ticket Followers (#441)

Staff and administrators can now **follow a ticket** (a Follow button on the ticket page, or `PUT` and `DELETE /api/v1/tickets/{id}/follow`) and are emailed about its activity: a ticket they follow being assigned, and its requester replying. This is new behaviour, not a change to anything that existed.

- **Followers are emailed under the *Activity on tickets I'm assigned to or follow* switch**, and the administrator's override of it (`staff_email_activity_override`) applies to them. Nobody is emailed about their own change, and nobody is emailed twice for one event whatever mix of assignee, group member and follower they are.
- **People only.** An API key, an OAuth client or a service user cannot follow or unfollow: `403 session_required`. Reading the list, `GET /api/v1/tickets/{id}/followers`, works for staff and administrators, and for a key with `tickets:read`.
- **Staff always see who follows a ticket.** There is no setting to hide the list.
- **Following is audited** (`followed`, `unfollowed`). Administrators see those entries on the ticket's Activity feed and in the admin-wide audit view. **Staff do not, until an administrator turns on the new setting `staff_can_view_follow_audit`** (Settings → General → Audit log: *Staff can view follow history*; off by default, session only). Requesters never see them.
- A follower who can no longer see the ticket (ticket scope was turned on, or their groups changed) is skipped when the mail is sent, and **keeps following**, so they are mailed again if they can see it again.
- **Reassigning a ticket ends the old assignee's follow.** If you followed a ticket you were assigned, and it is then assigned to someone else or to nobody (or your account is deleted, for tickets that are still open), your follow is removed and recorded as `unfollowed` with whoever moved the ticket as the actor. Click Follow again to keep the mail. Moving a ticket between groups does not touch anyone's follow.
- **The assignee is not mailed as a follower, and neither are the members of the assigned group when the group itself is mailed.** The group is mailed for an assignment, and for a requester's reply on a ticket nobody holds. For those, their own switches ("Tickets assigned to me", the group switch) decide, and following changes nothing. If somebody in the group holds the ticket, the group is not mailed for a reply, so a member who follows it is mailed as a follower.
- Database migration `000046` adds the `ticket_followers` table. It is safe to apply on a running instance. Migrations `000043` to `000045` belong to other changes in the same release. Upgrading from 1.2 straight to the final 1.3.0 applies all of them in number order. A database that ran a 1.3.0 beta build containing `000046` before those were merged is a different case: the migration tool only moves forward from the version the database is at, so it would skip `000043` to `000045`. Do not run such a beta database on the final release without checking that those migrations were applied.

## Reopening a Ticket (#464)

A reopen is any move from Resolved or Closed back to an open status: a requester's reply on a Resolved ticket (an account holder, or a guest), a status change by staff or an administrator (`PATCH /tickets/{id}`, or MCP `update_ticket_status`), and `POST /tickets/{id}/reopen`. Until now a reopen changed the status and nothing else. It now does five things, all in the same transaction as the status change:

- **The ticket is flagged.** `reopen_count` (new on the ticket JSON for staff and administrators only, omitted when 0, never reset) counts reopens, and staff and administrators see a **Reopened** badge on the ticket and in the queue while the ticket is open. It goes away when the ticket is resolved again. Tickets reopened before the upgrade start at 0.
- **The priority goes up one level on the first reopen only** (low, medium, high, critical; critical stays). This is a behaviour change on an existing action: a ticket that used to keep its priority through a reopen now gets a `priority_changed` entry and a new priority, and can match a different SLA policy. Later reopens do not raise it again.
- **The SLA starts over**, on every reopen (the priority rise is the first reopen only). The earlier record is kept, the ticket shows the **SLA restarted** badge (staff and administrators), and the audit log gets `sla_restarted`. Nothing happens to the SLA while SLA tracking is switched off. Before this, a reopened ticket kept its old record, which could already be met or breached.
- **Staff are emailed**: the assignee (under *Activity on tickets I'm assigned to or follow*), the assigned group's members (under *My groups' tickets*, only while nobody personally holds the ticket, as for a reply) and the ticket's followers (under the activity switch, whoever holds it), once each, and never the person who reopened it. A group member of a held ticket hears about a reopen only if they follow it. The administrator's overrides apply. **Once email is configured, staff who already receive staff email start receiving these.** Set the overrides to Off for everyone to keep their inboxes as they were.
- **A status change out of Resolved or Closed is now audited as `reopened`** beside `status_changed`, and **raises the `ticket.reopened` webhook event**, which it did not before. A webhook subscribed to `ticket.reopened` will see it for those changes as well as for a requester's reply and `POST /reopen`.

- **A priority change can no longer undo a restart.** If a ticket's priority is moved to one with no SLA policy and later to one with a policy, the new SLA record resumes from the clock it left, or from the ticket's last reopen if that is later (it used to start from the ticket's creation, which could erase a restart). A reopen is remembered as a restart point even when it lands on a priority with no policy. The older record's met and breach marks are not copied. A ticket that never had an SLA record still starts from creation.

- **Requesters no longer see the activity history.** `GET /tickets/{id}/audit` and `GET /tickets/{id}/history` now answer a reporting user `403`; before, a requester could read the audit feed (with assignment actors withheld) and the status timeline of their own ticket. The ticket page no longer shows them status-change events in the conversation either. Requesters see the ticket's current state and its public replies, as before. Staff and administrators are unchanged, and guests never had either route. A script that signs in as a requester and reads those two routes will now get `403`.
- **`reopen_count`, `pending_since` and `sla_paused_seconds` are staff-only**, in every ticket response including MCP. A requester no longer receives those keys. Staff and administrators are unchanged except that `reopen_count` is omitted when 0.

Database migration `000047` adds `tickets.reopen_count` (an integer, default 0), `last_reopened_at` and `paused_seconds_at_last_reopen`. It is safe to apply on a running instance.

## Admin API: data left behind, half-applied edits and path checks (#419)

Scripts and API keys that call the admin API can see these changes. Nothing needs doing in the admin UI.

- **Deleting a category, type or item also deletes its custom-field assignments** (a category's include those of its types and items). They used to be left behind, pointing at a record that no longer existed. Assignments left over from deletes made on earlier versions are removed once by migration 000039 (those whose category, type or item no longer exists); it cannot be undone, and the rows it removes pointed at nothing.
- **Deleting a group now writes an `unassigned` audit entry for every ticket it held**, open or not, in the same transaction as the delete. Before, the tickets silently lost their group with no entry. The entry's before and after values name `assignee_group_id`, which the audit view now shows.
- **`PATCH /admin/users/{id}` checks every field before it writes any.** `{"disabled": true, "email": "bad"}` answers 400 and leaves the account enabled; it used to disable the account, end its sessions and then answer 400. The same goes for an email another account holds (409) and a change that would remove the last active administrator (400): both are refused before anything is written, where `{"reset_mfa": true, "role": "user"}` on the only administrator used to clear their factors and then answer 400.
- **Soft-deleted accounts answer 404** to `PATCH /admin/users/{id}` (for any body, `{}` included, which used to answer 200 with the deleted record) and to `POST /admin/users/{id}/password` (which used to set the deleted account's password and answer 204).
- **Same order of checks for a session and an API key** on the password reset and on PATCH: the body is checked first (400), then the caller (403), then the account (404). Before, an API key aimed at an administrator or a deleted account got 403 or 404 before its body was looked at.
- **Mismatched ids in a URL answer 404.** `/categories/{id}/types/{typeId}` (PATCH, DELETE), the item routes under it, and all nine custom-field assignment routes now require every id to exist and to belong to the one before it, and an assignment to belong to the scope in the path. Before, only the deepest id was read, so a wrong parent id was ignored. Creating an assignment for a category, type or item that does not exist answers 404 (it used to store an assignment for nothing), and listing the items of an unknown type, or the assignments of an unknown category, type or item, answers 404 (it used to answer an empty list).

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

## Ticket and Settings API Responses (#420)

Changes a script or integration may notice:

- `POST /tickets` now answers with the ticket after routing, so `assignee_user_id` / `assignee_group_id` are filled in when a routing rule or the auto-assign setting applied. Before, they were absent until the ticket was fetched again.
- `POST /tickets/{id}/replies` now includes `author_name` in the reply, as the reply list does.
- `PATCH /tickets/{id}` with no body at all is now the same as `{}`: nothing changes, and the answer is `200` with the ticket. Before, it was `400`. Malformed JSON is still `400`.
- `PATCH /admin/settings` now refuses JSON `null` (`400`) for `attachment_scan_address`, `attachment_allowed_types` and the four `attachment_reputation_*_enabled` toggles. Before, `null` was stored and read as "unset" or "off". To clear the scanner address, send an empty string. To restore the default allowed types, send the list you want.
- The six choice settings in `PATCH /admin/settings` (`closed_reopen_policy`, `audit_mask_requester_names`, `attachment_scan_policy`, `attachment_infected_handling`, `attachment_mismatch_handling`, `attachment_reputation_refresh`) now answer their own error code for every refusal: `invalid_closed_reopen_policy`, `invalid_audit_mask_requester_names`, `invalid_scan_policy`, `invalid_infected_handling`, `invalid_mismatch_handling`, `invalid_reputation_refresh`. Before, `null` and a value of the wrong type (a number, a boolean) answered `bad_request` for most of them. The status is still `400`, and the messages are unchanged. A script that matched on `bad_request` for these needs to match the specific code instead.
- Every route under `/tickets/{id}` now accepts a tracking number (for example `GHD-2026-000001`) as well as a UUID. Before, only `GET /tickets/{id}` did; the others answered `400` with `bad_request` or `invalid_id` ("invalid ticket ID"). Those two `400`s no longer exist. A UUID works exactly as before.

## Webhooks: retries, delivery ids, history and notes (#448)

Several changes for anyone who receives webhooks. Two of them can break a receiver.

### Breaking: the raw payload is now ids only

A hook with payload format **Raw JSON** is sent only the event name (`Type`), `TicketID`, `ActorID`, `OccurredAt` and, where the event has them, a `Payload` with `new_status_id` or `target_id`. These are gone from the payload: `TrackingNumber`, `Subject`, `Priority`, `guest_email`, `reporter_email`, `ReplyBody`, and the `internal` and `link_type` fields. A receiver that reads any of them must fetch the ticket through the REST API using `TicketID`. Whether something is a reply or a note is now the event name (below).

Slack, Teams, Discord and Jira Automation hooks are not affected: their messages still carry the subject, tracking number, status and public reply text, and never an address or a note's text.

### Breaking: internal notes are their own event

`ticket.replied` is now sent for customer-visible replies only. Internal notes are sent as the new `ticket.note_added`. A hook subscribed to `ticket.replied` stops receiving notes; add `ticket.note_added` (or choose all events) to keep them.

### Failed deliveries are retried, so a receiver may see an event twice

A delivery that fails with a connection error, DNS or TLS failure, timeout, 5xx or 429 is tried again, 30 seconds doubling to an hour, 8 attempts in all. A `Retry-After` header from the receiver is honoured up to an hour. A blocked (internal) address and any other status, such as 400 or 404, are not retried. If you change a hook's URL, deliveries still waiting for the old address are dropped, and so are deliveries waiting for an event the hook no longer lists.

Every delivery now carries two headers: `X-GHD-Delivery`, a UUID that stays the same across the retries of one delivery (use it to drop a repeat), and `X-GHD-Event`, the event name. The signature headers are unchanged.

### New: delivery history and Redeliver

Admin, Webhooks has a History button per hook: its last 25 deliveries with the outcome, status, attempts and time. No payloads and no error text are kept. Redeliver sends a delivery's event again as a new delivery. A Slack, Teams, Discord or Jira message is rebuilt from the ticket as it is now, so it can differ from the original; a reply's text is sent only if the reply still exists and is public. Raw hooks get the same ids-only body again.

This adds a database table through a migration that runs at start-up.

## Email: SMTP Encryption and Honesty (Breaking, Read First)

Email (SMTP) now fails loudly instead of quietly. Several setups that used to "work" now fail, and the failure shows in the admin panel. Check these before upgrading. Nothing needs changing if your mail server offers STARTTLS (or you use port 465) and, when `SMTP_USER` is set, offers AUTH.

There is no new setting. The rule depends on the port and on whether `SMTP_PASSWORD` is set:

- **Password set:** TLS is required. Port 465 uses TLS from the first byte; every other port must offer STARTTLS. A password is never sent unencrypted, even to a private address.
- **No password:** plain SMTP is allowed only when the server's address is loopback, private (10/8, 172.16/12, 192.168/16) or IPv6 fc00::/7. A public address still needs TLS.

### What breaks

1. **A localhost relay that took a password without TLS now fails.** Before, the password went out in the clear to localhost when the relay did not offer STARTTLS (Go already refused to send a password in the clear to any other non-localhost relay). Now the send fails with `SMTP server does not offer STARTTLS, and SMTP_PASSWORD is set: refusing to send the password unencrypted`. Fix: turn on STARTTLS (or TLS on port 465) on the relay, or drop the password if the relay does not need one for this network.
2. **Unauthenticated mail to a public server without STARTTLS now fails.** Before, mail went out in the clear. Now: `SMTP server does not offer STARTTLS and is not on a private address: refusing to send mail unencrypted`. Fix: use a server that offers STARTTLS or port 465.
3. **`SMTP_USER` set but the server offers no AUTH now fails the send.** Before, the server was used anonymously and the mail went out (or was rejected later). Now: `SMTP server does not offer AUTH but SMTP_USER is set`. Fix: point at a server that offers AUTH, or remove `SMTP_USER` and `SMTP_PASSWORD` if the relay accepts your mail without a login.
4. **Turning on signup or guest submission is refused while email is off.** `PATCH /admin/settings` answers `400 email_required` when `self_signup_enabled` or `guest_submission_enabled` goes from off to on and `SMTP_HOST` or `SMTP_FROM` is unset. Installs that already have either on are not switched off and are not refused when they save other settings; they get a warning in the admin panel instead.
5. **A broken email template now fails the message at once.** Before, the message was dropped and nothing said so. Now the row is marked failed with no retries and the error appears in the admin email warning. (The shipped templates render; this only matters for a damaged build.)

Port 465 now connects with TLS from the first byte. Before, it hung until the timeout, so no working setup changes.

### How you will see it

Failed sends are retried for about an hour (a template error is not retried) and then marked failed. While any email message is retrying or failed within the last day, `GET /api/v1/admin/security-warnings` returns `email.failing: true` with the newest error text (addresses replaced by `[address]`). It also lists which of `SMTP_HOST` and `SMTP_FROM` is unset, and whether signup or guest submission is on with email off. The server log says once at startup whether email is enabled, and warns when only one of `SMTP_HOST` and `SMTP_FROM` is set.

## MCP `add_reply`: Public Replies Now Email the Requester

**Behaviour change.** Until now a public reply posted through the MCP `add_reply` tool was saved but never emailed to the requester, although DESIGN.md says it is and the REST reply does it. From 1.3.0 it is: the guest's address (with a guest link) or the reporting account's address, the same as a REST reply.

- Anything you have connected over MCP, including AI agents, can now send mail to your customers. Review which credentials have `tickets:write` before upgrading.
- Internal notes (`internal: true`) still send nothing.
- MCP has no `notify_customer` switch; a public reply always notifies. Use an internal note for anything that should not reach the requester.
- **REST change, staff and administrators only:** a staff member or administrator who replies publicly on a ticket they filed themselves is no longer emailed a copy of their own reply. Before, they were. This applies over REST and MCP. Webhook subscribers see it too: the reply event's `reporter_email` is empty in that case.
- **Unchanged for requesters:** an ordinary user replying on their own ticket still gets the email copy, as before. Replies on other people's tickets are unchanged.

## Webhooks: a ticket filter (#413)

A hook can now be limited to tickets in chosen categories, priorities or assigned groups (Admin, Webhooks, Ticket filter), or inverted to send for every ticket that does not match. A hook with no filter, which is every hook after the upgrade, behaves exactly as before. The raw payload is not changed by a filter.

It works together with the retries and history above, as follows. An event a hook's filter turns away is not a delivery for that hook: no `X-GHD-Delivery`, no retry, no History entry. The filter is read as it is now, not once. It is tested when an event is fanned out to the hooks, again at every attempt of a queued delivery, and again at Redeliver. If you tighten a filter, deliveries already waiting for a retry that it no longer admits are dropped at their next attempt, without a send or a History entry, the same way a retry is dropped when the hook is disabled, unsubscribed from the event or given a new URL. If you widen it, they still go out. A retry is tested against the ticket as it was when the event happened; a delivery queued before this upgrade has no ticket to test and is sent whatever the filter says. Redeliver tests the ticket as it is now, and is refused with `409 webhook_filter_excludes` when the filter does not admit it (if the hook has a filter and the ticket is gone, the filter cannot be checked and it is refused with `409 webhook_filter_unverifiable`; a hook with no filter is still sent). Nothing about this changes what a hook is sent: the raw payload and the chat messages are the same, and the ticket details used for the test are never put in a body or kept in the History.

`ticket.created` is raised before auto-assignment, so a group condition never matches it; subscribe to `ticket.assigned` to hear about tickets landing in a group.

This adds a column to `webhook_configs` through a migration that runs at start-up.

## Admin API: wrong or stale response bodies, and three decided rules (#419, response half)

Scripts and API keys that call the admin API can see these changes. Migration 000040 is new.

**Breaking**

- **Inactive statuses are retired for everyone.** `PATCH /tickets/{id}` with a `status_id` that names an inactive status now answers `409 status_inactive`, for staff, administrators and API keys alike; before, the move was accepted. Tickets already in an inactive status stay there and can move out, and sending a ticket the status it already holds is not a move. The ticket status picker no longer lists inactive statuses (the ticket's own status stays listed). If you deactivated `Pending` expecting SLA pausing to carry on, it now stops for new tickets; see the SLA section of DESIGN.md. If the configured reopen target status is inactive, every reopen (forced reopen, a requester's or guest's reply on a Resolved ticket) goes to New instead, administrators see a banner (`reopen_target` in `GET /admin/security-warnings`) asking for an active status, and the `PATCH /admin/statuses/{id}` that deactivates the target returns a `warning`.
- **API keys: who lists and revokes which.** (Security: goes in the release notes under Security, PR 8.) An API key could delete another administrator's key if it knew the id; that has been possible since 1.0.0 (1.0.0 to 1.2.0 are affected) and is revoke-only. Now an API key lists and revokes **only itself**: not its owner's other keys and not anyone else's (any other id answers `404`). An administrator signed in through the browser lists and revokes **every** key, so `GET /admin/api-keys` for a browser session now returns all keys instead of the caller's own, and each entry carries `owner_name` and `owner_email`. A script that used one key to list or revoke the others now needs a browser session.
- **Custom-field errors have their own codes.** A name already in use (create or PATCH of a definition) and a field assigned twice to one place now answer `409 conflict` (they were `400 bad_request` with the database's own text). A `field_def_id` that names no field answers `400 invalid_reference`. A database failure on these routes is a `500 internal_error`, not a `400`. Validation errors stay `400 bad_request` with the same messages. No response carries database text any more.
- **A failed ticket count fails the request.** `GET /admin/statuses` and `GET /statuses` answer `500` when a count cannot be read; before, the status was reported with `ticket_count: 0`, and the admin page treats 0 as safe to hard-delete.
- **Category, type and item names are trimmed on `PATCH`**, as they are on create. A name sent with spaces around it is stored without them, and the response is the saved row. Names already stored are not changed.

**Additive or corrected**

- **`auth_type` reports OIDC.** New values: `oidc`, `local+oidc`, `saml+oidc` and `local+saml+oidc`. `local`, `saml` and `both` (password plus SAML) keep their meaning for accounts without OIDC. Accounts that change value: an account that signs in only through OIDC, or has a password and OIDC, used to read `local`; an account with SAML and OIDC used to read `saml`; and one with a password, SAML and OIDC used to read `both`. A client that switches on `auth_type` with a fixed list of values needs to handle the new ones.
- **`visible_on_new` on a new field assignment:** the API default is still false when omitted. The database column default (it was true) is now false too (migration 000040); existing rows are untouched.
- **`GET /admin/sla/policies` is ordered by severity** (critical, high, medium, low, then policies with no priority), then by name. It was ordered by the priority text, which put `low` before `medium`.
- **`POST /admin/oauth-clients` returns `id`** (the record id that `DELETE` takes), beside `client_id` and `client_secret`.
- **`PATCH /admin/users/{id}` returns the real `passkey_count`**; it always said 0.
- **`PATCH /admin/webhooks/{id}` returns the saved row.** After a URL change `last_delivery` is `null`; it showed the old receiver's result.
- **`PATCH /admin/custom-fields/{id}` returns the saved, trimmed row.** `POST` and `PATCH /admin/statuses` return the status's real `ticket_count`; they said 0.
- **A blank webhook `secret` on `PATCH` still clears it**, by design and for administrators only; now documented in the API spec and DESIGN.md.

**Ordering.** This change adds migration **000040**, which follows 000039 (#432).

## Old Password Hashes Must Be Reset After 30 Days (#453)

1.3.0 hashes passwords at bcrypt cost 12 (earlier releases used 10). An account is upgraded automatically the next time its owner signs in. **Accounts that have not signed in within 30 days of the first start of 1.3.0 must reset their password:** a correct password on an old hash is refused with 403 `password_reset_required` and the message "Your password was saved in an older, weaker form and must be reset before you can sign in." until it is reset.

- **Clock:** starts when 1.3.0 first runs against the database (migration 000044 records it once).
- **N:** Settings → Authentication → Passwords, default 30, 1 to 365. Changing it moves the deadline for everyone.
- **Exempt:** all administrators, so an instance cannot lock itself out. They are upgraded at their next sign-in like everyone else. Staff and requesters are refused. When SAML restricts password sign-in to administrators, others get the SAML refusal instead.
- **How people reset:** "Forgot password" on the sign-in page once password reset by email lands (#451), or an administrator sets a new password for them. Until then only the administrator route works.
- **What to check:** people who sign in rarely (seasonal staff, requesters) are the ones affected. Tell them, or have an administrator reset them, before day 30.
- **API clients:** sign-in with an old hash after the deadline answers 403 `password_reset_required` instead of 200. New installs are unaffected.
