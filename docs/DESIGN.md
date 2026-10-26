# Go Help Desk — Design Document

## Overview

Open-source, self-hosted help desk system inspired by HESK, with local, SAML and OIDC sign-in, TOTP and passkey MFA, guest and self-service submission, a REST API and an MCP interface. A plugin infrastructure is specified (v2) but does not work yet. Built with a long-term roadmap toward SaaS (v4).

## Versioning Roadmap

| Version | Scope |
|---------|-------|
| **v1** | Core ticketing (linked tickets, follow-ups from Closed tickets, auto-assignment, optional SLA tracking), local + SAML + OIDC auth, self-service signup, guest submission, MFA (TOTP or passkey), custom fields, CTI-linked group management, canned responses, full-text search (Postgres FTS), attachments with malware scanning and hash-reputation lookup, admin audit view with retention, REST API, MCP interface, email + webhook notifications delivered through a database outbox (with Slack/Teams/Discord/JIRA payload formats and per-hook ticket filters), Docker deployment |
| **v1.4** | ITSM ticket types (Incident/SR/Problem/Change), Impact × Urgency priority matrix, default ticket type per CTI (#135); incident management (#408); monitoring events (#411); operational reporting (#131); asset reference (#415); continual improvement (#405); suppliers (#406); relationship management (#407) |
| **v2** | Plugin system (1st/3rd-party, sandboxed, admin UI install); plugin runtime (#416); Snipe-IT reference plugin (#403) |
| **v3** | Knowledge base, custom admin-defined roles |
| **v4** | Multi-tenancy / SaaS, plugin registry |

Custom fields, CTI-linked group management, canned responses and full-text
search were built ahead of the original v2 schedule and are shipped; this table
reflects that, not the order they were planned in. Plugins went the other way:
the admin plugin routes exist (there is no admin page for them) but nothing
registers a plugin, so the list is always empty and enable/disable fails for
every id; install/uninstall return `501` and no event ever reaches a plugin
(`plugin.Registry.Dispatch` is built and never called from the notification
chain). 1.x has no working plugin system; it is a v2 feature. The part of the plugin pitch
worth keeping in v1, chat/ITSM notifications, ships as payload formats on the
webhook feature; see Notifications.

---

## Tech Stack

| Layer | Choice | Rationale |
|-------|--------|-----------|
| Backend | **Go** | Single binary, small Docker image, strong concurrency, good WASM story for the planned v2 plugin system |
| Frontend | **React (Vite SPA)** | Large ecosystem, good theming via CSS variables, Shadcn/ui component library |
| Database | **PostgreSQL** | Row-level security for future multi-tenancy, JSONB for custom fields, native FTS, best managed hosting options |
| Deployment | **Docker** (`docker-compose`) | Go app + Postgres. Upgrade path to Kubernetes for v4 |

---

## Data Model

### Ticket Classification (Remedy-style Cascading)

Three-level hierarchy: **Category → Type → Item**

- Selecting a Category filters the available Types
- Type dropdown is disabled until a Category is chosen
- Selecting a Type filters the available Items
- Item dropdown is disabled until a Type is chosen
- Types and Items are optional downward — a Category may have no Types, a Type may have no Items

**Setup creates the first Category, so "required" is always satisfiable.**
Category is required on every ticket and categories, unlike statuses, are not
seeded. `POST /setup` therefore takes an optional `category` name (the wizard
pre-fills one; blank falls back to "General") alongside the administrator. The
category is created **before** the administrator and only when none exists:
setup answers 409 forever once a user exists, so a failure between the two
steps must not leave an instance with an administrator and no category. That
order leaves two failure modes, both retryable (nothing happened, or a category
exists and no user does), and the existence check makes a retry reuse the
category instead of stacking duplicates. See #323.

### Tickets (v1)

Core fields (all editions):

- **Subject** (required, short summary)
- **Description** (required, full detail of the request/issue)
- **Category** (required)
- **Type** (optional, depends on Category having Types defined)
- **Item** (optional, depends on Type having Items defined)
- **Priority** (a fixed set of four: `critical`, `high`, `medium`, `low`; the set is not configurable). Set at creation; staff and admin can change it afterwards (`PATCH /tickets/{id}`), which re-matches the ticket's SLA policy for the new priority without restarting its clock. Refused on a Resolved or Closed ticket (see SLA Tracking → Re-matching when priority changes, #443). On the ticket page it is a select in the **Details** card, shown to staff and admin only (a reporting user sees the badge), and disabled with a one-line reason on a Resolved or Closed ticket; a refusal shows the server's message and leaves the old value
- **Status** (customizable statuses per instance, see Ticket Lifecycle below)
- **Assignee** (staff member or group). On the ticket page, staff and admin get an **Assign to me** button in the Assignee panel, offered only when the ticket has no assignee user (#444): it is hidden once the ticket is assigned to anyone, me included, on a Resolved or Closed ticket, and for a reporting user. **It keeps the ticket's group.** The server's assign sets the user and the group from the request, so a user-only `PATCH` would clear the group; the button sends `assignee_user_id` together with the ticket's own `assignee_group_id` (the key is omitted when there is no group). Picking a user from the list, by contrast, replaces the group, as before
- **Attachments** (file uploads)
- **Replies/thread** (staff and user messages). **Append-only by design.** A reply cannot be edited or deleted, by anyone; a correction is a new reply. Decided on #468 (2026-10-13).
- **Linked tickets** (related, parent/child, caused-by, duplicate-of — can link to any ticket including Closed)
- **Tracking number** (quoted in notification email, and one half of a guest link re-request). Shaped `PREFIX-YEAR-SEQUENCE`, e.g. `GHD-2026-000001`. The prefix is the `ticket_prefix` setting (**Admin → Settings → General → Tickets**): one to eight uppercase letters or digits, `GHD` by default, validated when saved (`400 invalid_ticket_prefix`) and applied only to tickets created afterwards, because issued numbers are already in customers' inboxes and replies and are never rewritten
- **Resolution notes** (summary of what resolved the ticket, captured at resolution)
- **Source channel** (`tickets.source_channel`, #130): how the ticket arrived, one of `web`, `api`, `mcp`, `guest`, `phone` (a CHECK constraint; `email` is reserved for inbound mail and is a migration away). **Derived by the server from the entry point, never asserted by a caller**, because a caller-chosen channel is a column no report can trust:

  | Entry point | Channel |
  | --- | --- |
  | The web app's ticket form, or any REST call made in a browser session | `web` |
  | REST with an API key or OAuth client (the credential's `Machine` flag, not the route) | `api` |
  | MCP `create_ticket`, `create_follow_up` | `mcp` |
  | The guest form, and a guest's follow-up | `guest` |
  | Staff filing on someone's behalf from the web form, with "Phone call" picked | `phone` |

  **`phone` is the one value a person chooses**, and only a member of staff in a browser session who also names a requester (`reporter_user_id` or `reporter_email`, #444); the default stays `web`. `POST /tickets` takes a `source_channel` member for that alone. **Every other use is refused with `400` and nothing is written** (refusal rather than ignoring, as for a requester's `reporter_*`): any value from an API key or OAuth client, any value but `phone` from a session, `phone` without a requester, and MCP `create_ticket` with a `source_channel` argument. The ticket service backs this up: `phone` without a filer is a validation error. **A follow-up records its own entry point**, not its original's: the person who clicks follow-up is the one opening this ticket, by whatever route (a follow-up of a phone ticket is `web`). **Existing tickets read `web`** (migration 000056), which invents history for those that came in by API or MCP; the 1.3.0 upgrade notes say so. The column has no default after the migration, so a new entry point that forgets to name its channel fails the NOT NULL check instead of being recorded as `web`. **Staff and administrators see it** (the ticket object, MCP `get_ticket`, and a small "via phone" line next to "Opened" on the ticket page); **requesters and guests never do**: it is on the requester never list, because it is operational and `phone` would tell them that staff filed it. A filter on the ticket list is not built.

SLA fields (optional feature toggle, all editions):

- **SLA target** (response time and resolution time targets, configurable per Priority and/or Category)

ITSM fields (v1.4, #135; behind an admin setting, off by default, so an install that sorts work by Category alone sees no change):

- **Ticket Type** (Incident, Service Request, Problem, Change Request)
- **Impact** (High / Medium / Low — how broadly the issue affects the organization)
- **Urgency** (High / Medium / Low — how time-sensitive the issue is)
- **Priority** (overridden: derived from Impact × Urgency matrix instead of manual selection)
- **Default ticket type per CTI** — when ITSM is enabled, each Category/Type/Item combination can have a default ticket type configured (e.g. `Hardware > Laptop > Broken Screen` defaults to Incident)

### Users & Roles

Three roles: **Admin**, **Staff**, **User**

| Role | Capabilities |
|------|-------------|
| **Admin** | Full system access. Manage settings, users, groups, categories, tags. (Plugins are not manageable in 1.x; the plugin system is planned for v2.) Can always log in with local auth even when SAML is enabled (failsafe). |
| **Staff** | Create tickets. View/edit/assign tickets within their scope (see [Groups & Scope](#groups--scope)). Search tickets by tracking number, subject, or description keywords. Jump directly to any ticket by tracking number or UUID. Assign tickets to any staff member or group. Add and remove tags on tickets. |
| **User** | Create tickets. View their own tickets. Reply to and attach files to their own tickets until they are Closed; a Closed ticket is read-only to them (see [Closed is terminal](#closed-is-terminal-and-read-only)). Reopen a Resolved ticket by replying within a configurable window (admin setting: "Users can reopen tickets for X days after resolution"); the ticket is flagged, raised one priority level the first time, and its SLA starts over (see the Ticket Lifecycle). |

**Admin → Roles** is a read-mostly page: it shows the three roles with a one-line description each and, under each, the users who hold it, with a dropdown that changes a user's role (`PATCH /admin/users/{id}`, the same call the user page makes). It does not define permissions; the roles are fixed. The dropdown is disabled on administrator rows (demote an administrator from their user page) and it shows the first 200 accounts only: it calls `GET /admin/users` without `page`, which returns at most 200 (the paged mode below is what lifts that cap, and this page does not use it yet).

**Custom admin-defined roles (v3):** admins will be able to define additional roles and grant a curated set of permissions (e.g., "Tier 1 Agent" with ticket read/reply but no assignment rights). The three built-in roles remain as defaults and cannot be removed. Permissions are stored as discrete capability flags rather than hardcoded in code, with the built-in roles expressed as preset bundles so existing behavior is preserved.

### User Management (Admin)

Admins manage accounts from **Admin → Users**. The list is paged, 50 to a page, with a search box (a substring of the name or email, ignoring case) and a role filter, and shows "Showing 1-50 of N" with Previous and Next. `GET /admin/users?page=N` is what asks for that: it also takes `per_page` (default 50, maximum 200), `q`, `role` and `service_account` (`true` = only service accounts, `false` = only people), answers a bare array, and puts the number of matches in `X-Total-Count`; a bad `page`, `per_page`, `role` or `service_account` is a 400. **Without `page` the endpoint is unchanged** — the newest 200 accounts, with `limit`, `offset`, `q`, `role` and `service_account` ignored, as before — so an existing client sees no difference, and the 200 cap lifts only for a client that asks for later pages. The list is clickable — clicking a user opens a detail page with:

- **Profile** — edit display name, email address, and role. Changes take effect immediately.
- **Account info** — member since date, login type (Local / SSO (SAML) / SSO (OIDC) / Local + SSO), MFA enrollment status. The login type is read off what the account holds: a password, a SAML subject, an OIDC subject. The API reports it as `auth_type`: `local`, `saml`, `oidc`, `both` (password and SAML), or a `+`-joined combination that includes `oidc` (`local+oidc`, `saml+oidc`, `local+saml+oidc`).
- **MFA reset** — clears every second factor the user holds (the authenticator and all registered passkeys) and ends all of their sessions, so the user re-enrols on next login. Only shown when the user holds any second factor.
- **Enable / Disable** — disabled accounts cannot log in. Tickets and history are preserved. Re-enable at any time.
- **Password reset** — set a new password directly (shown only for accounts with a local password). No email link required for admin-initiated resets. The account holder can also reset their own by email (Authentication → Forgot password, #451); the administrator's route is unchanged and is the way back when email is off.
- **Groups** — view current group membership, add to groups, or remove from groups.
- **One edit, one outcome.** A single save from the detail page can change several fields (disabled, MFA reset, role, name, email). Every field is validated, the account is confirmed to exist, and the two refusals the database would otherwise give mid-save (a change that would remove the last active administrator, and an email address another account holds) are checked, all before the first write, so a refused save changes nothing. The writes themselves stay guarded statements, so a request that loses a race with another administrator is still refused by them, possibly after earlier steps have been applied. A deleted account answers `404` to every edit and to a password reset, and the checks run in the same order for a session and an API key.
- **Last administrator.** Disabling, deleting or demoting the last active administrator is refused (`400`): setup does not reopen, so an instance with none is orphaned for good. The count and the write are one statement that locks every active administrator row, so two parallel requests (even one administrator removing both another and themselves) cannot both pass. Removing an administrator's *ability to authenticate* is a different door the guard does not watch; see the identity provider guard and the end of Passkeys.
- **Service users** — the account an OAuth client acts as is listed here with a "Service" badge (`service_account`, `auth_type: "service"`). Password, MFA and passkey actions on it are refused with `409 service_account` (the page hides them); renaming it, changing its role between staff and user, giving it groups and disabling it work as for anyone. See [OAuth Clients and Service Users](#oauth-clients-and-service-users).
- **Delete** — marks the account deleted. It stops authenticating immediately, every session is revoked, and it drops out of the admin list; the row itself stays, because the tickets and replies that reference it do. Those keep the person's display name on them: a thread that renamed its participants after the fact would not be an accurate record of what happened. There is no hard delete and no anonymisation, so this is not the tool for a request to erase somebody's data. Requires a second confirmation click. Prefer disabling instead when there is any chance the account may be needed again.

### Ticket Lifecycle

```
New → In Progress → Pending (waiting on user/vendor) → Resolved → [reopen window] → Closed
                                                           ↑            |
                                                           └── Reopened ┘ (within window)

Closed is terminal by default: nothing leaves it. The way forward is a new,
linked follow-up ticket. An instance setting (closed_reopen_policy) can allow
admins, or staff and admins, to force-reopen; requesters never can.
```

- **Resolved**: ticket is answered/fixed. Starts the configurable reopen window.
- **Reopen window**: admin setting (`reopen_window_days`, shown as **Reopen window and auto-close**) — "Users can reopen tickets for X days after resolution." Requesters can add a reply to reopen during this window. **It is also when the ticket closes** (#349): the auto-close sweep closes a Resolved ticket once the window has passed, and a Closed ticket is read-only and a requester can never reopen it. **0 means no reopening, and the ticket is closed and read-only on the next sweep, about five minutes after it is resolved** — not "off". One window, for every kind of requester (guest and account holder alike).
- **Reopen target status**: the status a ticket is moved to when it is reopened. Configured from **Admin → Settings → General → Ticket lifecycle → Reopen target status** (a picker limited to active, non-system statuses). Defaults to the status named "New" when unset, not to the first active custom status. It is stored by name, so it can be deactivated after it was picked. An inactive status is retired, so every reopen (forced reopen, a requester's reply on a Resolved ticket, a guest's reply) goes to New instead, administrators see a banner from `GET /admin/security-warnings` (`reopen_target`) asking for an active status, and the `PATCH /admin/statuses/{id}` that deactivates the target carries a `warning` in its response.
- **Closed reopen policy**: admin setting (`closed_reopen_policy`, shown as **Reopening closed tickets** under Admin → Settings → Ticket lifecycle; session-gated like the other settings that widen who may do what). `off` (the default) | `admin` | `staff_admin`. **Closed is terminal by default; an instance setting can allow forced reopen by admins only, or by staff and admins; requesters never.** Unset, unreadable or unrecognised reads as `off`, never as a permissive value; an unrecognised value is refused (`400 invalid_closed_reopen_policy`) when saved. Read on every request, never cached, so a change applies to the very next request.
- **A reopen** ([#464](https://github.com/PubliciaLLC/go-help-desk/issues/464)) is any move from Resolved or Closed back to an open status. Every way of doing it is the same reopen: a requester's reply on a Resolved ticket inside the window (an account holder, or a guest through their link); a status change by staff or an administrator (`PATCH /tickets/{id}` with `status_id`, and MCP `update_ticket_status`); and `POST /tickets/{id}/reopen`. Leaving Closed still needs `closed_reopen_policy` to allow the caller's role, and a refused request changes nothing. Resolved to Closed and Closed to Resolved are not reopens. A reopen does five things, in the same transaction as the status change (`ticket.Atomic`), so none of them happens without the others:
  - **It flags the ticket.** `tickets.reopen_count` (migration `000047`) goes up by one and is never reset; the ticket JSON carries it as `reopen_count`, to staff and administrators only (omitted when 0). A requester never receives it; see *What a requester sees of a ticket* below. Staff and administrators see a **Reopened** badge on the ticket page and on the ticket's row in the queue (table and mobile cards) while the ticket is open, and it goes away when the ticket is Resolved or Closed again. A requester does not see it. Tickets reopened before the upgrade start at 0: reopens were only ever audit entries, which retention can prune, so there is nothing reliable to count from.
  - **It raises the priority one level on the first reopen only** (low to medium to high to critical; critical stays critical). "First" is the count being 0 before this reopen, so a ticket that is resolved and reopened again is flagged again but not raised again. The change goes through the same primitive as a staff priority change (`changePriorityInTx`, reason `reopened`) and writes `priority_changed` with that reason if the priority moved (a critical ticket writes nothing). A `PATCH` that reopens a ticket and also sends a `priority` raises it first and then applies the priority sent, so the value sent wins.
  - **It restarts both SLA clocks**, on every reopen and not only the first, at any priority: see *A reopen restarts both clocks* under SLA Policy Matching. Every reopen is also a **restart point** remembered on the ticket (`last_reopened_at` and the Pending total then, `paused_seconds_at_last_reopen`), including a reopen that lands on a priority with no SLA policy and so starts no record.
  - **It writes `reopened`** to the audit log (and `priority_changed` and `sla_restarted` as above). A status change out of Resolved or Closed now writes `reopened` beside its `status_changed`; before, only the reply and `POST /reopen` did.
  - **It emails staff and fires `ticket.reopened`**: see Notifications. A status change out of Resolved or Closed raises the `ticket.reopened` webhook event, which it did not before. The reopen and the status change are **one event** for the ticket (kinds `ticket.reopened` and `ticket.status_changed`, plus `ticket.priority_changed` on the first reopen, if the priority moved), written to the outbox in the same transaction; the mail and the webhook are sent a moment after the commit. See [Ticket Events: One Change, One Event](#ticket-events-one-change-one-event).
- **Closed**: automatic transition after the reopen window expires, or an administrator closing the ticket. **Archived read-only, and terminal by default** — see [Closed is terminal](#closed-is-terminal-and-read-only) below. Unless the policy above lets their role, staff and admin cannot reopen it either; they open a linked follow-up.

  **Auto-close scheduling.** This transition is driven by a periodic background
  sweep, not computed on read: a ticket sitting in Resolved past its window
  must actually flip to Closed even if nobody opens it again, because the
  point of Closed is "no further user updates," and a status nobody re-derives
  until the next page view can't enforce that. The sweep finds tickets whose
  `resolved_at` plus the reopen-window setting (in days, as configured at the
  time the sweep runs) has passed, and calls the same `Close` operation for
  each — attributed to "System" in the status history, per the existing rule
  above. It is safe to run repeatedly: a ticket that is already Closed no
  longer matches the query, so a re-run or an overlapping run does nothing to
  it. **A reopen window of 0 does not skip this transition** — it removes the
  grace period, not the close itself, so a ticket resolved under a 0-day
  window is eligible for auto-close on the very next sweep. The sweep interval
  is an implementation detail rather than a user-facing setting; a few
  minutes of slack between "the window elapsed" and "the ticket shows Closed"
  is immaterial against a window denominated in days. The sweep runs every
  five minutes from a goroutine started in `cmd/server/main.go`, alongside the
  session-expiry sweep. Each tick reads the reopen-window setting afresh and
  calls `ticket.Service.AutoClose`, which lists up to 500 candidates
  (`ListResolvedBefore`) and closes each through the same code path as
  `Close`. Under the row lock it re-checks that the ticket is still Resolved
  and still past the cutoff, so a ticket reopened between the query and the
  lock is left untouched: no write, no history row, no notification. Eligible
  tickets beyond the 500 are picked up on the next tick, and a failure closing
  one ticket is logged without stopping the rest. (Originally tracked as
  [#184](https://github.com/PubliciaLLC/go-help-desk/issues/184).) Auto-close
  **does not revoke the guest's link**: see below.
#### Closed is terminal and read-only

Decided in [#349](https://github.com/PubliciaLLC/go-help-desk/issues/349). A
Closed ticket is an archive, **terminal by default**. An instance setting
(`closed_reopen_policy`, above) can allow forced reopen by admins only, or by
staff and admins; **requesters never can**, whatever it says, and the follow-up
is available in every mode. The rule, for every route (REST and MCP) and every
credential (session, API key, OAuth client acting as the same role). The Staff /
Admin row is the default, `off`:

| Who | On a Closed ticket |
|-----|--------------------|
| Guest (link holder) | **Read only.** Reply, upload and anything that could reopen are refused with the generic `404`, byte-identical to a bad link (below). The one write that accepts a closed ticket is `POST /guest/follow-up`: a new, linked ticket (below). |
| Account holder (User role) | **Read only.** Reply, attachment upload, custom-field edit and linking are refused with `409 ticket_closed`; status change, assign, reclassify, resolve and close are refused with `403`, as for any state. They **can** open a follow-up of their own closed ticket (below). |
| Staff, Admin | **Cannot reopen, by default.** A status change out of Closed, Resolve, Resolve-as-duplicate and `POST /tickets/{id}/reopen` are refused with `409 ticket_closed`, and the message says why: reopening closed tickets is disabled on this instance, or restricted to administrators. With `closed_reopen_policy` = `admin`, an administrator may use all four (staff still get the refusal); with `staff_admin`, staff and administrators may. They **can** always open a follow-up, and keep replying, internal notes, assignment, reclassification, tags and linking (see below). |
| Resolved (any requester) | Unchanged: a requester's reply inside the reopen window reopens the ticket; outside it, `409 reopen_window_closed`. |

**One rule, in the domain.** The service decides it, not each handler: the
reply/upload lifecycle check (`lifecycleAllowsReply`) and the requester's other
writes (`Service.CanRequesterWrite`) both ask one function (`closedRefusal`);
guest writes resolve through `TicketForGuestWrite`. Every way out of Closed —
`Reopen`, `UpdateStatus`, `Resolve`, `ResolveAsDuplicate`, so the reopen
endpoint, the status route, resolve, duplicate-of with auto-resolve and MCP
`update_ticket_status` — asks one predicate, `ticket.CanForceReopen(policy,
role)`, through `Service.forceReopenGate`, **on the locked row**: the policy is
read there (`closed_reopen_policy`, wired by the server, no cache), after the
ticket's row lock is taken, so a setting flipped to `off` while a request was in
flight is the one that applies. A requester is `false` in every mode and is
refused earlier, by role, with the refusal a requester has always had (`403`),
never told about the setting. The earlier incident (GHSA-2x4f-j4jv-m2cm) was two
surfaces each deciding one rule on their own.

**A forced reopen** targets the existing reopen target status (**Reopen target
status**, falling back to New), through the shared timestamp rule, so the SLA
pause is carried and a closed/resolved timestamp is cleared, with a
status-history row and the ordinary reopen notification (a guest is sent a link,
issued at send time as for any reopen). The guest's links are not touched by it
(closing never revoked them), and the ticket is writable through them again as
soon as it is not Closed. The audit entry says it was forced and under which
policy: `reopened` for the endpoint, with `forced_reopen: true` and
`closed_reopen_policy` in its "after" (the same two keys ride on the
`status_changed` or `resolved` entry of the other doors). The ticket response
carries `can_reopen` (true only for a Closed ticket and a viewer the policy
allows) so the ticket page shows the Reopen button only to them; it is a
courtesy, and the service decides again when the button is used.

**Races: the rule holds against a close that wins.** A requester's reply or
attachment is checked once up front, and the row is written later (for an
upload, after the file has been read, scanned and stored). So the write
re-decides on the **locked** ticket row, in the same transaction as the insert
(`refuseRequesterOnClosed`, in `addReply` and `CreateAttachment`): a close that
committed in between refuses the write (guest: the generic `404`; reporter:
`409 ticket_closed`; for an upload the stored file is removed), and one that has
not yet committed waits for the write. Staff are not locked or refused. Link
adds already lock both rows. A reply that would have reopened a Resolved ticket
and lost the race to a close is refused too, so a closed ticket never gains a
reply from a requester. Pinned by deterministic interleaving tests in the domain
suite.

**What is refused with which status, and why.** A reporting user sees their own
closed ticket, so a clear `409 ticket_closed` for *their own* ticket tells them
nothing they cannot already read. It never becomes an existence oracle: a ticket
the caller may not see answers the not-found refusal first (see Authorization),
for replies, field edits and links alike. Writes that were already refused by
role (status, assignment, reclassification, priority, resolve, close, tags) keep their
`403`; the closed state does not change which refusal a role gets.

**Staff and admin: the minimal reading.** Only *leaving* Closed is removed.
Replying (including internal notes), assignment, reclassification, tags,
linking and attachments are unchanged on a closed ticket, because they do not
change what state the ticket is in, and removing them is not what was decided.
(A staff reply on a closed ticket still mails the guest a read-only link, below.)
**Priority is the exception:** it cannot be changed on a Resolved or Closed
ticket (`409 ticket_closed`), because a finished ticket's priority is part of
what its SLA was measured against (#443). A `PATCH /tickets/{id}` that sends a
priority and would leave the ticket Resolved or Closed (or that touches one
already there) is refused whole with that `409`: the status or assignment in the
same request is not saved first.

**One PATCH, one outcome (#468, #506).** `PATCH /tickets/{id}` can change a
ticket's status, assignment, classification and priority in one request. It is
one service call, `Update`, in **one transaction**, in that order (the priority
last, so a category changed in the same request is the one its SLA policy is
matched against). A refusal at any step leaves nothing saved and writes no audit
entry, and the request announces one event (Ticket Events: One Change, One
Event). The handler still decides every refusal it can before the transaction
starts: an invalid priority value, a priority or assignment or reclassification
from a role that may not make it, an invalid classification (`ValidateCTI`), a
priority on a ticket that would end up Resolved or Closed, an assignee that
cannot be given a ticket (unknown, deleted, disabled or a reporting user), a
group that does not exist. The handler makes no status check. `Update` itself
checks that the status exists and that the role may move to it (`statusFor`)
before it opens the transaction. Inside the transaction the steps check, on the
locked row, what each can: the assignment re-checks the assignee and group; the
priority step re-checks the role and the closed state; the status step checks
the reopen policy and an inactive status, for the first time and only there
(`updateStatusInTx`). The
classification step takes the row lock and does not check again: `ValidateCTI`
before the transaction is its only check. The assignment and reclassification
roles are not checked by the service: authorisation belongs to the caller, so
the handler does it first, and a caller other than this handler (MCP, the
scheduler) must do its own. A concurrent change between the early check and the
transaction (an assignee disabled, the ticket closed) or a database failure
makes the whole request fail and roll back, where before #506 the steps ahead of
the failing one had already been saved. Over MCP `update_ticket_status` answers a move out of Closed with the closed
refusal and names the follow-up instead.

**Follow-up.** The way forward from a Closed ticket, in every `closed_reopen_policy`
mode. It creates a **new** ticket from the closed one, through the same creation
path as any ticket (`Service.create`): the same validation, tracking number,
opening status history, audit entry (carrying `follow_up_of`), "created"
notification and routing. REST applies the auto-assignment rules like a new
ticket; MCP's `create_ticket` does not, so neither does `create_follow_up`.
Three doors, one creation path:

| Who | Route | Notes |
|-----|-------|-------|
| Staff, admin | `POST /api/v1/tickets/{id}/follow-up`; MCP `create_follow_up` | Any closed ticket they can see. Not limited. |
| Account holder (and an API key or OAuth client acting as one) | `POST /api/v1/tickets/{id}/follow-up` | **Their own** closed ticket (a ticket they cannot see is not found; one they can see but did not report is `403`). |
| Guest | `POST /api/v1/guest/follow-up` | The ticket their link names, only while it is Closed. |

- **Copied for staff and admin:** subject, description, category / type / item,
  priority, and the requester: the reporting user, or the guest's address, name
  and phone. A guest is told, as for any new guest ticket, with a link to the new
  ticket.
- **Copied for a requester — only what a requester may set on a normal create.**
  The principle: a requester's follow-up must not be a ticket they could not have
  created directly. So: subject, description, category, the type for an account
  holder (never for a guest), and who they are. **Not** the item (requesters
  cannot pick one), and **not the priority** — it is medium, as for any requester's
  new ticket; a priority staff raised stays with the closed ticket. No assignee,
  tags or SLA state either. The category (and the type) must still be active, as
  for a normal create (`400` if an administrator has since archived it). The
  request body is ignored: there is no field to set.
- **Not copied, for anyone:** replies and internal notes, attachments, status
  history, custom-field values, tags, the assignee, any SLA state. The new ticket
  starts in New and runs its own SLA clock. Custom-field values are not copied
  because they live outside the ticket service, and copying them on one surface
  and not the other would be two rules; a later change can add them to the
  service for both.
- **Linked:** the closed ticket is the **parent** of the follow-up
  (`parent_child`, source = closed ticket), written in the same transaction as
  the ticket, so a follow-up never exists without its link. The existing link
  types are reused; no new relation or table. A requester seeing the link on
  either ticket sees only their own tickets.
- **The original is not written:** still Closed, same replies and history, and a
  guest's links untouched.
- **Only from a Closed ticket** (`409 ticket_not_closed` otherwise): an open
  ticket needs no way forward, and allowing it would make this a general clone
  that two live tickets can drift apart from.
- A follow-up can itself be closed and followed up.

**A requester's follow-up and abuse limits.** It must not be a way around the
limits a normal ticket has, and the action must not become a stream of tickets:

- **One per closed ticket for a requester** (`409 follow_up_exists`; for a guest
  the generic `404`). A follow-up staff opened counts; staff are not limited. The
  check is made before a tracking number is taken, and again on the locked
  original in the transaction that writes the ticket and its link, so two clicks
  make one ticket. To go on, follow up the follow-up once it is closed.
- **Account holders** are held to what a direct create is: there is no rate
  limit on an account holder creating a ticket, so none is added; the cap above
  is what bounds this door.
- **Guests** are held to what `POST /guest/tickets` is: guest submission must be
  on (otherwise the generic `404`) and the same per-address budget applies,
  **shared with direct submissions**, not a second bucket (`429`).

**The guest route and its refusals.** `POST /guest/follow-up` is a deliberate
exception to "every guest write route refuses a closed ticket": it resolves the
link through the **read** lookup (a closed ticket resolves) and then requires
Closed itself. Every other outcome is the same generic `404`, byte-identical to a
bad link's refusal on the other guest write routes: an open ticket, a bad or
expired link, guest submission off, a follow-up already opened. The order is
fixed so nothing is told in between: Closed first, then the submission switch,
and only then the per-address budget, so a `429` is only ever seen by a holder of
a good link to a closed ticket. The new ticket is a guest ticket for the same
guest (address, name and phone from the original), and **its own link goes by the
ordinary guest-ticket-created mail** — not in the response, which is the new
tracking number and nothing else, as for a normal submission. The audit entry has
no actor for a guest, as a normal guest ticket's has none; the identity is the new
ticket's own guest columns, and `follow_up_of` says what it continues.

**Known limits** (recorded, not fixed, in #349):

- **Follow-up mail wording.** A guest's follow-up sends the ordinary
  `ticket_created` mail ("We have received [TICKET]") for a ticket the guest did
  not file themselves. A follow-up-specific line needs a new typed field on the
  notification event, which the outbox record must round-trip (#346's code);
  email never reads `Payload`, deliberately. The mail still carries the new
  tracking number and a working link, so the guest can follow it.
- **A lost double-submit burns one tracking number.** A requester's follow-up
  is checked for an existing one before a tracking number is taken, and again on
  the locked original in the transaction that writes the ticket. Two requests in
  flight at once both pass the first check and both take a number
  (`NextSeq`); the loser is refused at the locked check, so its number is never
  used and the sequence has a gap. One gap per lost race, never a second ticket.
- **"The follow-up" is any parent_child link out of the closed ticket.** The
  one-per-closed-ticket cap cannot tell a follow-up from a link someone made by
  hand: the link row has no marker, a marker would be a schema change, and an
  audit entry is the wrong place (the retention sweep deletes it, and the cap
  would quietly lapse). So a staff member who links a closed ticket as the
  parent of another ticket uses up its requester's one follow-up. This is the
  conservative direction (it can only refuse a requester, never admit a second
  ticket), staff are unaffected, and a follow-up of the new child still works;
  pinned by a test so that changing it is a decision.
- **An archived category or type is a 400 on the guest route.** Every refusal
  that would say something about a link or a ticket's state is the generic 404,
  but a guest follow-up of a ticket whose category (or type) an administrator has
  since archived answers `400` with the reason, as a direct submission does. The
  caller already holds a good link to a closed ticket and knows the category it
  was filed under, so nothing is revealed that they could not see.
- **Deleted reporter.** A follow-up of a ticket whose reporting account has been
  deleted fails validation (`400`): creation requires a reporter or a guest
  address, and loosening that for follow-ups only would weaken the rule for the
  path every ticket takes.
- **Tokens accumulate on a closed ticket.** One per mail sent on it (each reply
  staff add, each resend), each expiring thirty days after it is issued. Expired
  token rows are not swept anywhere today, closed or not.
- **Requester follow-up on the ticket page.** The button stays after a reload
  and a second click is refused with a message (`follow_up_exists`); the page
  does not look the existing follow-up up for a requester, who is not shown the
  linked-tickets panel. After creating one the page offers a link to the new
  ticket rather than navigating to it.
- **Staff composer.** The ticket page hides the reply composer on a closed
  ticket for every role, though the API still lets staff and admin reply to,
  annotate and attach to one (the decision above). Staff do that over the API or
  MCP, not from the page.
- **Custom-field edits are not atomic with a close.** A reporter's custom-field
  edit is checked once (`CanRequesterWrite`) and then written value by value
  through the custom-field service, which is outside the ticket transaction. A
  close landing after the check leaves **all** the values in that request
  written on a closed ticket, and a close landing mid-loop leaves the remainder.
  Replies, uploads and links, which carry the thread, are atomic.

- Statuses are customizable — admins can add intermediate statuses, but
  New, Resolved and Closed are system statuses with special behavior. The
  server finds them by name at startup and compares lifecycle rules by
  name, so a system status's name is fixed: the admin API refuses to rename
  one (403), just as it refuses to deactivate one. Color and sort order
  remain editable.
- Custom statuses can be **deactivated** (hidden from new-ticket flows) and
  **reactivated**. Inactive means retired, for every role: the ticket status
  picker does not list it, and the server refuses a move into it with
  `409 status_inactive`, whoever asks (staff, administrators and API keys alike).
  A ticket already in an inactive status stays there and can be moved out. The
  reopen target status (see Ticket Lifecycle) is no exception: if it is deactivated,
  reopened tickets go to New and administrators are warned. They can only be **deleted** when no ticket is in that
  status and no status-history entry references it; otherwise deactivate
  it. System statuses can never be renamed, deactivated or deleted.
- Every status transition is recorded in a **status history** timeline and displayed on the ticket detail page interleaved with replies, in chronological order, **to staff and administrators**. A requester sees the ticket's current state and its conversation, not its history: `GET /tickets/{id}/history` and `/audit` answer them `403` (Erik, 2026-10-15), and the page shows them no timeline events. Events include: the old and new status names (with colors), who made the change (user display name or "System" for auto-close), and the timestamp. The initial status assignment at ticket creation is also recorded.

### What a requester sees of a ticket

(Erik, 2026-10-15; the list below was approved by Erik on 2026-10-15.) A requester sees the **current state** of their ticket and its conversation, not its history and not the staff's working data. This is an **allowed list**, applied to the response on its way out (`ticket.FilterForViewer` in `internal/domain/ticket/requester_view.go`), not a list of fields to remove: a field added to a ticket is hidden from requesters until it is added to the list on purpose. Staff and administrators get every response as built.

A requester is any account that is not staff or an administrator, including a role the code does not recognise. Every response that carries a ticket goes through the filter: `GET`, list, `POST`, `PATCH` and the other ticket routes, the follow-up, and every MCP tool that returns a ticket (`get_ticket`, `list_tickets`, `create_ticket`, `assign_ticket`, `update_ticket_status`, `create_follow_up`). Inside each ticket object, whatever the shape (one ticket, a list, a ticket wrapped with extra fields), only these keys are kept:

| Key | Why a requester gets it |
| --- | --- |
| `id`, `tracking_number`, `subject`, `description` | the ticket itself |
| `status_id`, `priority` | its current state, both shown on the ticket page |
| `category_id`, `type_id`, `item_id` | its classification, shown in the page's Classification panel |
| `created_at`, `resolved_at` | when it was opened and when it was resolved (the page shows both) |
| `assignee_user_id` | who holds it, as an id only; a requester has no route that turns the id into a name (the staff list and the user routes are staff-only), though a public reply by that person carries their name as it always has |
| `resolution_notes` | sent to requesters before the list existed; their visibility is unchanged |

The wrapper keys a response may add around a ticket are kept as they are: `sla` (already null for a requester), `can_reopen` (always false for a requester), `replies` (already without internal notes) and `links` (ticket ids and a link type). Any other wrapper key is dropped.

**Never sent to a requester**: `assignee_group_id` (which team holds it), `reporter_user_id` (their own id; the page never reads it), `guest_email`, `guest_name` and `guest_phone`, `closed_at` and `updated_at` (history-like timestamps: when it was archived, when anything last touched it), `pending_since` and `sla_paused_seconds` (when and for how long the ticket waited: audit information, not state), and `reopen_count` (staff information), and `source_channel` (how it arrived: operational, and `phone` says staff filed it for them). The conversation, the audit feed and the status timeline are separate: the conversation stays (public replies only), the other two are staff-only routes.

**Guests** (the `/guest` routes) are shown less: a strict subset of the same list, `tracking_number`, `subject`, `description` and `created_at`, plus the status's *name* and the replies. (A guest used to receive `updated_at`; it is gone, because a requester does not receive it and the guest list stays a subset.) The guest ticket response goes through `ticket.FilterForGuest`, so there is still one definition.

**Tests.** `TestRequesterKeys_EveryTicketKeyIsClassified` fails when a ticket field is on neither the allowed list nor the never list. `TestRequesterResponses_*` walks the router and calls every route a requester can reach (the router's own list, so a new route is included) with an empty body, against a ticket with every field filled; only create and follow-up are also called with a real body that succeeds. `TestGuestResponses_*` does the same for the guest routes. Both fail if a never-listed key appears anywhere in a body, or a ticket-shaped object carries a key off the list. The MCP tools are not on the router: the test takes its list from `mcp.TicketTools`, and a tool that is not classified in `internal/mcp` fails that package's test. Emails are not JSON and carry only the tracking number and a link to the ticket.

### Ticket Followers

([#441](https://github.com/PubliciaLLC/go-help-desk/issues/441), part D of #464.)
Staff and administrators can **follow** a ticket to hear about its activity without holding it. A follower is emailed under the staff-email rules (Notifications below), and the ticket shows everyone who follows it.

- **Who can follow: people only.** A staff member or administrator, signed in. An API key, an OAuth client and a service user cannot follow or unfollow: following is a personal choice, and a script acting for someone is not them (#490). The routes answer `403 session_required` to a machine credential, and `ticket.Service.Follow` independently refuses any account that is not an active, non-service staff or admin account, so a second caller (MCP, an importer) cannot get round the route. A requester cannot follow and has no route to the list.
- **A follower must be able to see the ticket** when following: the routes sit behind `requireTicketAccess` like everything under `/tickets/{id}`, so a ticket out of scope (#391) is a 404, and a tracking number works as well as a UUID. At send time the person is checked again (`Server.StillEntitled`, `CanViewTicket`): mail for a ticket they can no longer see is skipped, and **the row is kept**, so following works again if their scope returns. A deleted or disabled account, a service account and a requester are never mailed (`ListMailableTicketFollowers`).
- **API.** `PUT` and `DELETE /api/v1/tickets/{id}/follow` follow and unfollow for the caller; both are idempotent and answer 204. `GET /api/v1/tickets/{id}/followers` lists them, oldest first, for staff and administrators (a machine credential with `tickets:read` may read it).
- **Staff always see the current list of followers** on the ticket page, with a Follow / Unfollow button. No setting hides it.
- **Following is audited.** A follow and an unfollow each write a `followed` / `unfollowed` entry with the person as actor, in the same transaction as the change (`ticket.Atomic`); repeating either writes nothing. The entries are visible to **administrators**. A staff member sees them only when the administrator turns on **`staff_can_view_follow_audit`** (off by default; a disclosure-widening toggle, so session-only like `staff_can_view_ticket_change_history`). A requester never sees them. Both views leave them out in the query (`audit.Filter.ExcludeActions` for the admin-wide view, in the page and the total alike, so asking for them there by `action` returns nothing for staff; `audit.ActionFilter` for the per-ticket feed). The per-ticket feed also keeps them out of its 500-entry cap: they are fetched on their own, with a cap of their own, only for viewers who may see them, and merged by time, so toggling follow can never push a ticket's real history off its feed. A viewer who may see follow entries always gets their `before` and `after` (redacted like any diff), even without the change-history setting, so a removal on reassignment can say whose follow it was.
- **Reassigning takes the old assignee's follow with it** (Erik, 2026-10-14: the assignee follows the ticket by being assigned, so a follow on top is dropped when they lose it). When a ticket is assigned away from someone, or unassigned, or its assignee's account is deleted (open tickets only, as account deletion only unassigns those), their follow row is removed in the same transaction (`dropAssigneeFollow`, from `Service.Assign` and `Service.UnassignForUser`, which every route reaches: PATCH, auto-assign, MCP `assign_ticket`, account deletion; there is no bulk assign). It writes an `unfollowed` entry with the reassigning actor as the actor, after the `assigned` / `unassigned` entry; its before and after carry `assignee_user_id` (whose follow went, and who holds it now), and the feed labels it "Follow removed on reassignment (name)". No follow row, no entry. The same person again changes nothing. Only the assignee loses a follow: moving the ticket between groups, with the assignee kept or not, leaves group members' follows alone. They click Follow again to keep the mail. A per-user preference for this is not built.
- **Data.** `ticket_followers (ticket_id, user_id, created_at)`, primary key `(ticket_id, user_id)`, both columns cascading on a hard delete (users are soft-deleted, so the send-time check does the real work), migration `000046`.

### Ticket Activity Feed

`GET /api/v1/tickets/{id}/audit` (#129) answers "who changed this ticket, and when" from the audit log the domain layer already writes, without database access. Shown on the ticket detail page next to the status timeline. **Staff and administrators only** (Erik, 2026-10-15: requesters and guests see the current state of the ticket and its conversation, not its history or its audit trail): the route sits behind the same role gate as tags and followers, so a requester gets `403 forbidden` on a ticket they can see, and `404` on one they cannot, as for every route under `/tickets/{id}` (`requireTicketAccess` answers first). `GET /tickets/{id}/history` is gated the same way. Guests have neither route. Public replies stay visible to requesters; internal notes stay hidden.

This route only *reads* the audit log; it does not change what the domain layer writes to it. What gets written is narrower than "every mutation": `created`, `status_changed`, `assigned`, `unassigned` (an assignment cleared, or a departing user's tickets returned to the queue, #326), `resolved`, `closed`, `followed` and `unfollowed` (Ticket Followers above; withheld from staff unless `staff_can_view_follow_audit` is on, and from requesters always), and `reopened` (every reopen: a requester's reply that reopens a Resolved ticket, a status change out of Resolved or Closed, and `POST /reopen`; a move out of Closed is a force-reopen allowed only by `closed_reopen_policy` (#349) and says so with `forced_reopen` and the policy in its "after"), `priority_changed` (#443: the "after" says why the priority changed, `manual` or `reopened`, what happened to the ticket's SLA, and the ids of the SLA policy and record in force before and after) and `sla_restarted` (#443, #464: written by a reopen that replaces an SLA record, with the id of the record it replaced and, when one starts, the new one; a reopen that starts a first clock on a ticket that had none replaced nothing and writes nothing; a staff priority change never writes it). A reply and an internal note are recorded too, as `reply_added` (a public reply) and `note_added` (an internal note). Each is written in the same transaction as the reply, by its author; a guest's has no actor. The entry carries only `reply_id` and `internal` in its "after" and never the text (`audit.Redact` allow-lists exactly those two keys). The feed shows it as a sentence such as "Erik Test added an internal note", a requester's name is masked like any other action's, and a reply by a deleted account reads "Someone". Replies and notes written before this existed are not backfilled. Reply and note entries are fetched on their own, with a cap of their own (like follows), so a long conversation cannot push a ticket's history off its feed. CTI reclassification and custom-field edits are not recorded. Those are gaps in what `ticket.Service` writes, not something this route introduces.

(The paragraphs below about what a *reporting user* sees in this feed describe the code's response shaping, which a requester no longer reaches: the route is refused to them. They still apply to the staff and administrator views where they mention the diff.)

That inherited gate is enough for the entry itself (status, priority, subject, assignee are already visible on the ticket to anyone who can view it), but not for the *actor* on `assigned`/`unassigned` entries specifically: assignment is staff/admin-only, and unlike every other action here, nothing else on the ticket discloses that actor's identity to a reporting user — the ticket's own `assignee_user_id` is a bare UUID, and `GET /api/v1/staff`, the only place that resolves one to a name, is itself staff/admin-gated. So the API withholds `actor_id`/`actor_name` on those two actions when the caller is a plain reporting user, the same way `ticket.VisibleReplies` withholds internal-note authorship from that same viewer. Every other action's actor is fine to show as-is, subject to requester-name masking below (see `StatusHistoryEntry.ChangedByName`, which already does, via `/history`).

The field-level before/after diff is a second, independent gate on top of the feed itself: admin always sees it; staff only when `staff_can_view_ticket_change_history` (an admin setting, off by default) is on; a reporting user never sees it, regardless of that setting. What is shown is filtered by `audit.Redact`, an allow-list described under Admin-Wide Audit View below: the same rule applies to this feed. The feed itself is reached with `tickets:read` by a machine credential (it holds ticket entries only, never MFA or password resets); the admin-wide view needs `audit:read`.

### Admin-Wide Audit View

`GET /api/v1/admin/audit` (#129) answers the same question across every entity, not one ticket at a time. Staff and admin only; a reporting user has no route here. A signed-in session needs no scope. An API key or OAuth client needs the **`audit:read`** scope, which an administrator grants on purpose ([#362](https://github.com/PubliciaLLC/go-help-desk/issues/362)). `tickets:read` does not reach it, because for an admin's key the view covers every entity, MFA and password resets included.

**The audit view is built not to display protected or sensitive data**: PII, and data under FERPA, HIPAA, SOX or PCI-DSS ([#362](https://github.com/PubliciaLLC/go-help-desk/issues/362)), with one configurable exception, requester names, where `audit_mask_requester_names` leaves them shown (Actor names, below). This governs what the view displays, not what its viewer can find out: an entry's ids still lead to the records they name, under those records' own access rules. The before/after diff in both this view and the per-ticket feed is an **allow-list** (`audit.Redact`). It shows ids, statuses, priorities, flags and counts (and, on a factor reset run from the command line, the operating-system user, host and `source: cli` it records), and **leaves out** everything else: the ticket subject, which is free text, any nested value, and any field a writer adds later until it is allow-listed on purpose. Left out rather than shown as a placeholder, because the subject is written into both sides of every ticket entry, and a placeholder on both sides read as an edit that never happened. A key whose name looks like a secret (the deny-list of stems such as `password`, `secret`, `token`, `hash`, or a name ending in `key` or `pass`) is the exception: it shows as `[redacted]`, so a rotated secret still shows as a change.

**Names in the diff** (#422). A diff holds ids, and an id says nothing to a reader (and a deleted status leaves one that names nothing). Each entry in both views therefore carries an optional `names` object beside `before` and `after`: it maps an id, exactly as it appears in the diff, to what it names **now**: `{"name": "In Progress"}`. The stored entry and the `before`/`after` values are unchanged, so the trail stays exact, and the response only gains a key. An id with no entry in `names` no longer resolves (a deleted status, a deleted account); the UI then shows the id, and otherwise shows the name with the id as its tooltip. Only `status_id` (a status) and `assignee_user_id` (a user) are resolved, because they are the only id fields the allow-list lets through. Category, type, item and assignee-group ids are neither written to audit entries nor allow-listed; whoever allow-lists one adds it to the resolver (`auditNameFields`, `internal/server/audit_names.go`) in the same change. The names are built from the diffs **as they are about to be sent**, after `audit.Redact`, which is what keeps every existing rule in force: a viewer who is not shown the diff (staff with the change-history setting off, a reporting user) gets no `names`; only entries on the page are looked at, so a staff member's ticket scope is untouched and no name arrives through an entry outside it; a field that is not allow-listed is not resolved. A user who would be masked as an actor is masked here: `{"name": "Requester", "masked": true}`, by the same `audit_mask_requester_names` setting for the view (`admin_log` or `ticket_log`), the same current-role rule, and the same `masked` flag, so the setting cannot be bypassed by reading an assignee out of a diff. The lookup is one batch per page: one read of the (small) statuses table and one `id = ANY(...)` query for the users, never one per entry. A lookup that fails costs the names, not the page, and is logged.

**Actor names.** Staff and admin actors are always shown by name: the audit trail exists to say which employee did what. A requester (a student, a patient, a customer) is shown as **"Requester"** instead of by name in the views the setting `audit_mask_requester_names` names (Admin → Settings → General → Privacy, "Mask requester names in Audit Trail"): `admin_log` (the admin-wide log only), `ticket_log` (each ticket's Activity feed only) or `everywhere`, **the default**. Unset or unrecognised reads as `everywhere`, and the settings dump reports a stored value it does not recognise as `everywhere` too (see REST API → Settings dump and choice-list settings; [#366](https://github.com/PubliciaLLC/go-help-desk/issues/366)). A masked entry keeps its `actor_id`, so it stays attributable for an investigation, and carries `actor_masked: true` beside `actor_name: "Requester"`, so an account whose display name is "Requester" is not mistaken for a masked requester; the UI shows a masked actor as *Requester (name hidden)* with an eye-off icon, which a display name cannot reproduce. Whether an actor is a requester is read from their role **when the entry is viewed**, not when it was written: a staff member later changed to the user role has their earlier entries shown as "Requester", and a requester promoted to staff has theirs shown by name. **The mask is display-only.** It keeps requester names off the audit list and whatever is copied or captured from it; it does not stop identification. Anyone who can see an entry may still be able to find out who the requester is. When the masked actor is the ticket's own requester, the usual case, the ticket its `entity_id` names shows them on its page. An administrator can look up any masked actor from its `actor_id` (`GET /admin/users/{id}`). It is not an access control. The setting is auth-critical: loosening it shows requester names, so a machine credential cannot change it. The ticket page itself still shows the requester's name.

Staff are narrowed twice, independently. `entity_type` is forced to `ticket` whatever the query string asks for: every other entity type is admin-only. And with ticket scope enforcement on, only entries on tickets the staff member may see are returned. A third, smaller narrowing: `followed` and `unfollowed` entries (#441) are left out for staff unless `staff_can_view_follow_audit` is on, by the query itself (`ExcludeActions`) so the page, the offset and the total still describe one sequence. The view takes a repeatable `exclude_action` parameter that leaves those actions out, and the page offers it as **Hide replies and notes** (`reply_added`, `note_added`). Like the follow withholding below, it is applied in the query, so the page, the offset and the total describe one sequence; it only narrows, so it needs no gate.

**Scope is part of the query, not applied to its result.** The scoped page statement carries the filters (`entity_type`, `action`, `actor_id`, `from`/`to`, `q`) and the scope together, checked against `tickets`, and the scoped count behind `total` applies the same filters and the same scope predicate. The page, the offset and the total therefore describe one sequence: `total` is the number of entries the viewer can page through and says nothing about the ones they cannot see. Counting before scope was applied in Go told a staff member how many entries existed on tickets they could not see, and with the actor, action and date filters a count plus bisection dates activity on those tickets.

The scope predicate is the one the scoped ticket listing uses (`ListTicketsFiltered`): a ticket is visible to a staff member if they reported it, are assigned it, their group is assigned it, or it falls in a Category/Type a group of theirs covers. The store reads that staff member's groups and rules once per request and passes them to the query as values; nothing is looked up per entry. Staff with scope enforced and everyone else run different statements: an administrator's query has no scope in it at all. There are still two statements of the rule, `ticket.CanView` and the SQL (stated twice in the SQL, in the scoped page query and in the scoped count, word for word), and `TestAdminAudit_ScopeParity` holds all of them equal, page and `total` alike, so a change to one that is not made to the other fails it. The SQL's type rule pairs nothing: it checks the ticket's type against the rules' types and, redundantly but deliberately, its category against those rules' categories, which lets that arm use an index; it is exact because migration 000013's (category, type) foreign keys tie every type to one category. Removing the category condition changes no result and costs about 450,000 buffers on a sparse staff page.

Non-ticket entries and entries whose ticket no longer exists are never returned to a scoped staff member. To the person asking, an entry on a ticket they may not see and an entry on a ticket that does not exist are the same thing: both answer with nothing, so the view cannot be used to learn which ticket ids exist (the same policy as `404` for a ticket the caller may not see; see **Authorization** under "MCP Interface", which also records the REST `404` change, #174). Entries with the same timestamp are ordered by id, so the order is exact. Paging is by offset, so entries arriving between two page requests shift the next page, as with any offset pager. With scope enforcement off (the default) staff may see every ticket, so no scope is applied: they get the admin's query restricted to ticket entries, with entries on tickets that no longer exist included.

**What bounds the cost** (#331, where the measurements are). The page reads `limit + 1` rows; the extra row is how `has_more` is known, so paging never depends on the count. The count is bounded: it stops after 10,001 matches (`audit.TotalCap`, 10,000). `total` is exact up to and including 10,000; beyond that it is 10,000, `total_capped` is `true` and the page shows "10,000+". `total_capped` is always sent, `false` included, and a total that merely equals the cap is `false`. Admin and staff run different statements with the same filters and the same cap, so the cap applies to both alike. The reason is that retention is off by default, the table only grows, and an exact `COUNT(*)` over it is a full scan on every page view. Limits to know about:

- A filter that matches few rows still reads the table to find that out. `q` is a substring match no b-tree serves, so no index was added for it; `action`, `actor_id` and `created_at` already have one.
- The staff statements are planned from the staff member's own groups and rules, so the plan follows how many tickets they see (#331): someone who sees a handful of tickets is answered through the tickets indexes and the log's entity index; someone who sees most of them, with a narrow filter, through the filter's index and a check of each row's ticket; with no filter, by reading the log newest-first. Measured on 524,000 entries and 30,000 tickets, every staff count and page in #331's table was under 30 ms and most under 3 ms. This relies on PostgreSQL planning each request with its values. The generic plan for these statements is much slower, and `plan_cache_mode=auto` was checked not to choose it; a server forced to generic plans (`plan_cache_mode=force_generic_plan`) would lose this.
- A filter that matches many entries for a staff member who sees a mid-sized slice of tickets still reads entries until the cap or the page is found; the cap bounds the count, the page reads `limit + 1` visible entries. Deep offsets cost what reading that far costs.
- Under a generic plan PostgreSQL's JIT can add hundreds of milliseconds to the first runs of a large scan. That is the server's `jit` setting, not a property of the statements.

**Known, accepted side channel: response time.** Bulk hidden activity inside a date window is detectable by timing (measured before the count was bounded: about 5,000 hidden rows in a window took roughly 12–13 ms for the page plus about 12 ms for the count, against about 1 ms for an empty window; 20 hidden rows were indistinguishable from none). That is roughly two orders of magnitude smaller than the signal of the walk it replaced and the same class as the scoped ticket listing's. It does not reveal which tickets exist or anything about their content, and the response itself (entries, `total`, `total_capped`, `has_more`) is identical for hidden and missing tickets. Recorded in #331, not re-measured since. The plans changed in #331's second round; the side channel was not re-measured with them.

**Retention is opt-in and off by default.** `audit_retention_days` (admin setting) governs a daily sweep that hard-deletes anything older, with no archive table. The first sweep runs two minutes after start rather than only on the 24-hour tick, so an instance restarted more often than daily still prunes, and startup logs the window when one is set. **Unset, zero, negative or unreadable means keep forever**, which is what every release before retention did by having nothing prune at all. The default is deliberate: a 365-day default would delete the first year of history on any instance older than that, a day after upgrading, from a setting the operator never touched (the "existing behaviour must not change" rule), and an audit log is the worst thing in the system to shorten by accident because it cannot be reconstructed. The upper bound is **36,525 days** (`admin.AuditRetentionMaxDays`, a Gregorian century): the settings endpoint refuses anything above it, and a stored value above it (written before the check existed, or directly in the database) is read as the cap, because a cutoff computed from a larger number can overflow into the future and delete everything.

`audit_retention_days` is **auth-critical** (`admin.AuthCriticalKeys`), so a machine credential cannot change it. Shortening retention is the one setting that destroys evidence rather than merely widening access: set it to 1 and tomorrow's sweep removes every `mfa_reset` and `password_reset_by_admin` entry, so a leaked API key that performed a credential reset could erase the record of having done it. That is #306's reasoning about `ResetMFA` ("the exact action an attacker would want unrecorded") applied to the record rather than the act.

### Tags

Free-form labels that staff can attach to any ticket. Rules:

- Tags are **case-insensitive** and always stored **lowercase**.
- Any staff member or admin can add a tag to a ticket. **Creating a tag happens automatically on first use** — there is no separate "create tag" step.
- If a staff member types the name of a **deactivated tag**, the system returns an error explaining that only an admin can restore it. Staff cannot recreate a deactivated tag under the same name.
- **Admins** can deactivate (soft-delete) any tag from the Tags admin panel. Deactivated tags are hidden from autocomplete suggestions but remain on tickets that already have them (for historical accuracy).
- **Admins** can restore a deactivated tag, making it usable again.
- Autocomplete is available when adding a tag — as the user types, active tags matching the prefix are suggested.
- Tags are a flat namespace — no hierarchy, no parent/child relationships.

### Ticket Search

The ticket list includes a live search bar with a 300 ms debounce:

- Searches **tracking number** (prefix match — e.g. `GHD-2025-0` matches all tickets in that series), plus **subject** and **description** via Postgres full-text search (`tsvector`/`tsquery`), ranked by relevance.
- The query is tokenized into words and each word is prefix-matched (e.g. `print jam` requires a word starting with "print" **and** a word starting with "jam", in any order) — this is what keeps "search as you type" working on partial words, not just whole ones.
- Subject is weighted higher than description, so a match in the subject line ranks above one buried in a long description.
- Results are ordered by relevance rank (highest first), then by creation date — a tracking-number-only hit (no content match) ranks after every content match, ordered by recency among itself.
- Results are fetched as you type, with no minimum length. Fetching is shown inline with a spinner.
- **Staff and admin** can submit the form to perform a direct **tracking number / UUID jump** — navigates immediately to the ticket if found, or shows an inline error.
- Users only see results from their own tickets.
- **Staff and admin** searches cover **every ticket the viewer can see**, whatever queue is selected: all tickets, or, for staff with `ticket_scope_enforced` on, only what their scope admits (the same rule as everywhere else, not a second definition). The results area says "Searching all tickets you can see", and a one-click chip, "Only <queue>" (for example "Only my tickets", "Only unassigned"), narrows the search to the selected queue; clicking the chip again widens it. The label shows on every queue; the chip is not offered on an administrator's All queue, which is already everything. Spaces alone count as an empty box, and the chip resets to off when the box is cleared or the queue changes, so every new search starts wide. With an empty search box the queue behaves as it always did. The page sends `scope=visible` for the wide search (see **List filters**).
- Reply bodies are not indexed — only ticket subject and description. Deferred: searching reply content, fuzzy/typo-tolerant matching, and per-user saved searches.
- Uses Postgres's `english` text search configuration, which drops common English stop words (e.g. searching just "IT" matches nothing) — an accepted tradeoff of FTS, not a bug.

**List filters.** `GET /api/v1/tickets` takes `q` (above) plus `limit` (default 100, maximum 200) and `offset` (see Other protections), and these filters, checked in this order:

- `reporter_id=<uuid>`: the tickets one account reported. **Admin only** (`403` otherwise); the ticket list's "Filtering by client" chip, linked from the user page, uses it.
- `assignee_group_id=<uuid>`: the tickets assigned to one group. Staff and admin (`403` for a reporting user). With `ticket_scope_enforced` on, a staff member may only name a group they belong to (`403` otherwise): group ids are listed to every staff member, so without that the filter would hand over tickets the scope refuses everywhere else.
- `scope=mine|unassigned|all`: `unassigned` (no assignee user or group) and `all` are **admin only** (`403`). `mine`, the default, is the tickets assigned to the caller or to any of their groups; with scope enforcement on, staff get everything their scope admits instead, so an unassigned ticket in a covered category is not invisible to the people who should pick it up. A reporting user always gets their own tickets.
- `scope=visible`: staff and admin (a reporting user gets their own tickets, as with any scope). Every ticket the caller can see, in one query with ordinary `limit`/`offset` paging: every ticket for an admin or for staff with `ticket_scope_enforced` off, and the staff scope with it on. This is what a search with text in the box sends unless the "Only <queue>" chip is on. It reuses the existing `ListAll`/`SearchAll` and `ListVisibleToStaff`/`SearchVisibleToStaff` queries; no new index or migration.

The ticket list page offers the scope as Mine / Unassigned / All buttons to administrators, an "Include closed" toggle (Closed tickets are hidden unless it is on), and a status filter, and pages 50 at a time.

**Bulk status change.** On a desktop-width screen staff and admin can tick tickets in the list and apply one status to all of them. There is no bulk endpoint: the page sends one `PATCH /tickets/{id}` per ticket, so each goes through the same transition rules and authorisation as a single change and some can be refused (a move out of a Closed ticket, for one). The page then reports "n of m updated, k refused: reason" and leaves the refused tickets selected. Bulk selection does not exist on a phone (see Small screens).

### Linked Tickets

Tickets can be linked to any other ticket regardless of status (including
Closed) — by staff and admin. A requester cannot add a link to or from a Closed
ticket (#349). A follow-up opened from a Closed ticket is linked to it as its
child (`parent_child`, see Ticket Lifecycle). The backend (`ticket.LinkType`; `GET`/`POST /tickets/{id}/links`
and `DELETE /tickets/{id}/links/{targetId}/{linkType}` in
`handler_tickets.go`) is the canonical spelling, and the frontend's
`LinkType` in `frontend/src/api/types.ts` uses the same four values. Links
are viewed, created and removed from the **Linked tickets** panel on the
ticket detail page (`LinkedTicketsPanel.tsx`), which is shown to staff and
admins. (Wiring the UI and reconciling the enum was
[#185](https://github.com/PubliciaLLC/go-help-desk/issues/185); the
duplicate-of auto-resolve option was
[#186](https://github.com/PubliciaLLC/go-help-desk/issues/186).)

Link types — four, not five. A link is directional (source ticket → target
ticket), and that direction *is* the parent/child distinction rather than a
separate value for each direction:

- **Related to** (`related_to`) — informational association, symmetric
- **Parent / Child** (`parent_child`) — hierarchical grouping (e.g. a Problem
  with multiple Incidents). The ticket the link is created *from* is the
  parent; the ticket it is created *to* is the child. There is no separate
  `parent_of` / `child_of` pair — a link's direction already says which end is
  which, and a UI renders "Parent" or "Child" by comparing the ticket it's
  displaying against the link's source and target, not by storing two types
  for one relationship.
- **Caused by** (`caused_by`) — causal relationship, directional (source was
  caused by target)
- **Duplicate of** (`duplicate_of`) — marks the source ticket as a duplicate of
  the target. Creating this link offers a checkbox, **"Also resolve this
  ticket as a duplicate"** — when checked, the source ticket is transitioned to
  Resolved in the same action, with its resolution notes pre-filled as
  "Duplicate of `<target tracking number>`" (staff can edit before saving).
  Unchecked, the link is recorded with no status change. This is the
  "optionally" in "optionally auto-resolves the duplicate" — it is a choice
  made per link, not an instance-wide setting.

**Resolve all children** (#443). On a ticket that is the *source* of at least
one `parent_child` link (the parent), the Linked tickets panel offers **Resolve
all children** to staff and admin. It asks for resolution notes once (required),
then resolves the children **from the browser, one at a time**, each through the
ordinary `POST /tickets/{id}/resolve` with the same notes. There is no bulk
endpoint: each child therefore gets its own audit entry, notifications and SLA
handling, and the closed-ticket rules apply to it as to any resolve. A child
that is already Resolved or Closed is **skipped and never re-resolved**,
because a second Resolve with different notes overwrites the notes the child
already carries (#225). A refusal on one child does not stop the rest. The
panel reports `n of m resolved, k already resolved, j refused:` followed by each
refused child's tracking number and the server's reason, the same shape as the
ticket list's bulk status change; the notes stay in the box after a partial
failure, so a retry is one click and only touches the children still open. A
ticket that is only a child, or has only related, caused-by or duplicate-of
links, does not show the button.

**Direct children only** (decided on #505, 2026-10-15). The button resolves the
parent's *direct* children and nothing below them. Its count is the number of
direct children that are **not** Resolved or Closed: with four children of which
two are done it reads "Resolve 2 children" and says the other two are skipped,
and when none is open it is disabled with "All children are already resolved."
Each child is read again right before its own resolve, since its state can
change while the form is open. Once the form is open the panel reads each direct
child's links (one `GET /tickets/{id}/links` per child, nothing before the form
opens and nothing below the children) and, when some of them are themselves the
source of a `parent_child` link, says so: "2 of these children have children of
their own (tracking numbers, listed up to five); resolve those from their own
ticket." A child whose links cannot be read gets no notice, because the page
cannot say what it does not know.

### OAuth Clients and Service Users

Decided on [#434](https://github.com/PubliciaLLC/go-help-desk/pull/434) for
[#420](https://github.com/PubliciaLLC/go-help-desk/issues/420). An OAuth client
does not act as nobody, and it does not act as "staff in general". It acts as
**its service user**: one account per client, made with the client in the same
statement, named after it, and soft-deleted with it.

A service user is an ordinary `users` row with `service_account = TRUE`, and a
database CHECK (`users_service_account_inert`) that no code has to remember:
it can never be an administrator and never hold a password, a TOTP secret, a
SAML subject or an OIDC subject. `oauth_clients.user_id` is `NOT NULL UNIQUE`
(one user per client, never shared, never a person's account). Migration 000041
makes one for every client that already exists, with the staff role and no
groups, so with ticket scope enforcement off an upgraded client behaves as it
did.

| | |
|---|---|
| **Role** | `staff` or `user`, set by an administrator on the Users page; the actor's role is the stored one. Never `admin` (CHECK, and a 409 `service_account` before it). |
| **Signs in** | Never. Password login, SSO adoption by address, password reset, passkeys and MFA enrolment all refuse it, and no session for one loads. The login lookups (`GetUserByEmail` and the two subject lookups) do not return it; the session lookup skips it. |
| **Seen** | Listed on **Admin → Users** with `service_account: true` and `auth_type: "service"` (the page shows a "Service" badge and a filter for it). It can be renamed, given groups, disabled and set to staff or user, and nothing else: `PATCH /admin/users/{id}` answers `409 service_account` to a body carrying `email`, a `role` of `admin`, or `reset_mfa: true`, and applies none of it (a `reset_mfa: false` asks for nothing and is accepted; a `role` of `staff` or `user` is accepted, Q2 on #434). The page never sent the refused ones; the server now refuses them too. Renaming it renames its OAuth client in the same statement, so the two names cannot drift. The password route (`POST /admin/users/{id}/password`), MFA reset and API keys issued on its behalf are refused as well. Disabling it stops its client on the next request, and enabling it again restores it. It **cannot be deleted on its own**: `DELETE /admin/users/{id}` answers `409 service_account` with "This is the service account for OAuth client <name>. Delete the client instead.", because a client left listed with a dead user is worse than a refusal. It is left out of assignee pickers (`GET /staff`), assignment (`IsAssignableUser`, so round-robin skips it) and the plain user list. |
| **Writes** | Under its scopes, a client creates tickets and replies **as its user**. The audit trail and authorship show the client's name. |
| **Mail** | Never. A service user's address is made up (`oauth-<client id>@service-account.invalid`, which can never receive mail), and requester mail is not queued for a ticket it reported. Staff notifications go out as normal. |
| **Scope** | Ticket scope enforcement (`ticket_scope_enforced`) applies to its user like any staff member's: it sees what its groups cover. Turning enforcement on warns, on that save, which clients have a user in no group; the Security warnings banner (`oauth_clients_without_groups`) keeps saying so until each has a group or is disabled. |

**Recorded decisions** (the shapes below look wrong and are not):

- **`BearerAuth` keeps `Machine = true` although the actor now has a real
  user id.** `Machine` is what gates scopes and what closes `/me/password`,
  `/me/mfa/*` and `/me/passkeys*` to a credential. A user id must not open
  them, and `GET /me` for a client stays `{machine, role, scopes}`: its check is
  on the client id, not the user id.
- **`user_identity_required` still exists and no OAuth client can hit it.**
  `requireUserIdentity` and the MCP `hasUserIdentity` stay as the refusal for
  any credential that arrives with no user. The OAuth tests that pinned the
  refusal were rewritten to pin the opposite, on purpose.
- **The service user is created by `CreateOAuthClient`'s one statement, not by
  `user.Service.Create`.** Two writes that can half-apply are what the one
  statement removes; a client with no user cannot act, and a user with no
  client is an orphan.
- **The last-administrator guard is unchanged.** It counts `role = 'admin'`,
  the CHECK means a service user is never one, and so it neither props the
  guard up nor trips it. The note that a password is always a way in stays true
  for administrators for the same reason. A service user is the first account
  that has *no* way in, deliberately, and the CHECK is what keeps it out of the
  guard.
- **A client row that points at a person is refused by `BearerAuth`**
  (`service_account` must be true), because the schema alone would let it hand
  a client someone's identity.
- **The Security warnings banner and the save warning are both kept.** The
  warning on save is the moment the administrator can act; the banner is for
  the next day, when they have forgotten.

### Groups & Scope

- A **group** is a named pool of staff members
- A staff member can belong to **multiple groups**
- Groups are scoped to **Category/Type pairs**:
  - A group can be assigned to specific Category + Type combinations
  - Or assigned to an entire Category (implying all Types and Items under it)
- **Items do not factor into scope** — staff in-scope for a Type see all Items under it
- Scope is derived **exclusively from group membership** (no direct Category assignment to individual staff)
- Solo admin scenario: assign all categories to a single group
- **Deleting a group** takes every ticket off it, open or not, and writes an `unassigned` audit entry for each one naming the administrator, in the same transaction as the delete. If any part fails, nothing is applied. The group row is locked first, so a ticket cannot be assigned to it between the unassignment and the delete.

Scope does two separate jobs: it can restrict what staff see, and it routes new tickets.

**Visibility: `ticket_scope_enforced`, off by default.** Set under **Admin → Settings → General → Ticket visibility** ("Limit staff to their group scope"). With it **off**, every staff member can open every ticket, which is how every release before it behaved and why it is not on by default: switching it on during an upgrade would hide tickets from people mid-conversation. (Staff's default ticket list still shows the tickets assigned to them or their groups.) With it **on**, a staff member sees a ticket if they reported it, are assigned it, one of their groups is assigned it, or it falls in a Category/Type a group of theirs covers. That is the union of two readings that both applied (a ticket in your area that nobody has picked up is visible, and so is one assigned to you outside your usual area), and staff can take any action on tickets they can see. Administrators always see everything and a reporting user sees their own. One rule serves REST, MCP and the audit views (`ticket.CanView`, `CanViewTicket`/`TicketVisibility`, and the SQL held equal to them by tests). Staff in no group see almost nothing, so turn it on once groups and their scopes are configured.

**Routing: auto-assignment.** A newly created ticket is routed once, at creation, by the first rule that matches (the assignment is made by the system, so its audit entry has no actor):

1. A group whose scope covers the ticket's Category (or Category + Type). The ticket is assigned to that group; if several match, the first by name.
2. `auto_assign_group_id`: one catch-all group for everything the rule above did not match.
3. `auto_assign_user_ids`: a list of staff the tickets are dealt out to round-robin. The rotation is in memory, per server process. A listed user the assignment refuses (deleted, disabled or no longer staff) is skipped and the next one tried, so a departed colleague does not silently take every Nth ticket out of the queue.
4. Otherwise the ticket stays unassigned.

Routing is best-effort in one respect: a candidate that cannot be given a ticket (a departed colleague still on the round-robin list, a group that no longer exists) is skipped for the next, and if none is acceptable the ticket is left unassigned and the creation succeeds. A database error while checking a candidate is not skipped: it aborts the create. It runs for tickets created through `POST /tickets` and for follow-ups (`POST /tickets/{id}/follow-up`, `POST /guest/follow-up`). It does **not** run for MCP `create_ticket` and `create_follow_up`, and a new guest submission (`POST /guest/tickets`) does not call it either. `auto_assign_group_id` (a UUID string) and `auto_assign_user_ids` (an array of UUID strings) are set on **Admin → Routing** (a group picker and a staff checklist, with the rules above listed in order) or through `PATCH /admin/settings`. Saving is checked: a group id that names no group, or a user id that names no one or someone who is neither staff nor an administrator, is refused with 400 `invalid_auto_assign` and nothing in the request is written (before #447 such an id was saved and then skipped at routing time). An empty value clears the setting.

### Branding

- **Site name** — the product name shown in the sidebar header and browser title. Defaults to "Go Help Desk".
- **Logo** — uploaded via **Admin → Settings → Branding**. Accepted formats: PNG, JPEG, GIF. Max 2 MB. Images are proportionally scaled to fit within **320 × 64 px** and re-encoded as PNG. When set, the logo replaces the site name text in the sidebar. **SVG is not accepted** (#165): the logo is the only upload rendered inline in this origin, and pattern-matching SVG for scripts is a game you have to keep winning. An SVG logo uploaded by an earlier release stops being served after the upgrade — the route serves PNG and nothing else, so the sidebar falls back to the site name. The settings page says so where the logo would be: the file picker no longer offers SVG, a sentence explains why it went, and an instance whose stored logo can no longer be loaded is told that rather than shown an empty space.
- Both settings are stored in the database and managed via **Admin → Settings → Branding**.
- A public `GET /api/v1/site` endpoint returns `{name, logo_url, version, guest_submission_enabled, guest_attachments_enabled}`, plus `guest_attachment_max_bytes`, `guest_attachment_max_per_ticket` and `guest_attachment_uploads_per_hour` while guests may attach (#445) — no authentication required, so the shell and the login page render correctly before login (`guest_submission_enabled` is what decides whether the login page links to `/submit`; it reveals only what an unauthenticated `POST /guest/tickets` would reveal by answering 404). `guest_attachments_enabled` and the three figures are different: `POST /guest/attachments` answers the same `404` whether the setting or the token failed, so it never says whether uploads are on, but `/site` does, together with the effective per-file limit, to anyone. That disclosure was accepted (#445): it is low sensitivity, and it keeps the guest ticket response's allow-list unchanged.
- A public `GET /api/v1/logo` endpoint serves the stored logo file with a 5-minute cache header. `logo_url` in the site response points here when a logo is uploaded.

### Update check (opt-in, #170)

An administrator can have the server check for newer releases. It is **off unless chosen**, on a fresh install and on an upgraded one alike, and **while it is off the server makes no request at all**. That is how this respects an air-gapped or no-outbound deployment: DESIGN.md has no separate no-outbound rule (the only other outbound call is the reputation lookup, which is also opt-in), so there is nothing to configure, and the setting never needs to be hunted down and disabled.

- **Setting.** `update_check_enabled` (boolean; PATCH refuses any other JSON type, null included; unset reads as off), under Settings → General → Updates. It is in `AuthCriticalKeys`, so an API key or OAuth client cannot change it (403 `session_required`): it decides what leaves the server, the same family as the reputation toggles. Choosing it is a decision for a signed-in person.
- **The request.** At most once every 7 days, `GET https://api.github.com/repos/PubliciaLLC/go-help-desk/releases/latest`. Its own headers are exactly `User-Agent: go-help-desk` (fixed, **no version**) and `Accept: application/vnd.github+json`; no query string, body, cookie or identifier. Ten-second timeout. GitHub learns that an IP address fetched the file, once a week, and nothing about the instance, which is what the checkbox says ("contacts GitHub once a week; sends nothing about your server"). A change to what is sent must change that wording; a test pins the exact headers. Issue #170 asks for no version in the User-Agent because a version turns "an instance exists here" into "an instance running 1.2.0 exists here", a list of applicable CVEs. An earlier draft of this feature sent the version; it does not.
- **When.** A background goroutine started in `cmd/server/main.go` wakes hourly and does nothing unless the setting is on and the stored `checked_at` is more than 7 days old. Rendering an admin page never causes a request. A restart cannot re-check, because the time is stored (`update_check_result` in the settings table: `checked_at` and the newest stable `latest`). `checked_at` is written **before** the request, detached from the caller's context, so a process killed mid-request (SIGTERM, a crash loop) cannot ask again on restart. A failed check consumes the window too, so an instance with no egress does not retry hourly; the previous answer is kept. If the record cannot be read at all, that tick is skipped rather than treated as "never checked".
- **What counts.** The endpoint already skips drafts and prereleases, and the checker ignores a response flagged either. The tag must be exactly `v?MAJOR.MINOR.PATCH` with no leading zeros, no `+build` part and no prerelease; anything else is no answer. What is stored and shown is rebuilt from the parsed numbers (`%d.%d.%d`), never the tag's text, and the link is built from that, so a hostile response cannot choose where an administrator is sent. The stored value is re-checked the same way when read. Versions are compared as semantic versions against `internal/version`, so `1.3.0-beta` is behind `1.3.0` and `1.10.0` is ahead of `1.9.0`.
- **Failure.** A 4xx or 5xx, a timeout, bad JSON or a tag that is not a version is one log line at INFO (`checking for a new release failed`), as #170 asks, and nothing else.
- **What an administrator sees.** `GET /admin/security-warnings` carries `update: {current, latest, url}` when a newer release is stored and the check is on. `InsecureConfigBanner` adds one `info` entry (id `update:<version>`) to the `ServerNoticeBar`: "Version X is available (you run Y). Release notes", the link opening in a new tab. Being information it never turns the bar red, is not counted as a problem and is announced politely (`role="status"` when it is the only notice). The URL is built from the tag, never taken from the response. Its Hide button reads "Hide until the next release": unlike the bar's session hides it is remembered per browser and per version (`localStorage`), so the next release brings it back. Administrators only; the query is not made for anyone else. Nothing is ever installed.

### Post-upgrade wizard (1.3.0)

An install upgraded from an earlier release has never been asked about the choices 1.3.0 added. The first administrator who signs in lands on the dashboard and is sent to `/after-upgrade`, a short wizard with one step per question and **Back**, **Next** and **Skip** on each. A step shows today's setting, says what changed in 1.3.0 and lets the administrator change it.

- **Steps, in order:** the weekly update check (unchecked); staff email notifications (migration 000052 turned the three overrides off); staff API keys (`staff_can_create_api_keys` and the default lifetime slider); the old-password reset window (`password_rehash_grace_days`, with how many accounts are still on an old hash and whether email is set up). Guest file uploads (#536) is a fifth step, shown only while guest submission is on: it says that 1.2 let guests upload through the API and 1.3.0 switches that off, and offers `guest_attachments_enabled`, the size slider (`guest_attachment_max_mb`, held to the 25 MB instance maximum) and the per-ticket and per-hour limits, reusing the Settings controls. With guest submission off the step is not part of the wizard and the final step says guest uploads stay off because they need guest submission. Skipping it leaves uploads off.
- **Saving.** Next sends what the step changed through `PATCH /admin/settings`, so every server-side validation and every session-only rule applies unchanged. Skip sends nothing and drops that step's edits, so the setting keeps its safe default. A refusal is shown on the step and the administrator stays on it.
- **Upgraded, not guessed.** The wizard is shown while `post_upgrade_130_seen` is not true. Setup writes it `true` on a fresh install (setup asks the update question itself), so a fresh install never sees the wizard; an install that upgraded to 1.3.0 never ran that setup and has no value. The wizard writes it, in the same request as the last step, when it is finished or skipped, and it never shows again. This needs no migration and no version comparison. An administrator who closes the tab part-way sees it again next time, with the steps already saved showing their new values.
- **Who.** Administrators only. A staff member or requester is never redirected and the route turns them away; `/after-upgrade` redirects to the dashboard once the flag is set. Only the dashboard redirects, so a deep link after sign-in is not interrupted, and a failure to read the settings lets the administrator straight through.
- **Small screens.** One column, wrapping buttons, no horizontal scroll at 320 px (an e2e test pins it). Each step has one `h2`, focus moves to it when the step changes, and "Step N of M" is announced.
- **Setup.** The `/setup` form has an unchecked "Check for new releases (contacts GitHub once a week; sends nothing about your server)" box; `POST /setup` takes `update_check` (absent means false) and stores it as `update_check_enabled`.

---

## Authentication

### Local Auth (Default)

- Username/password with bcrypt hashing at **cost 12** (`user.ProductionBcryptCost`, #453). Releases up to 1.2 hashed at cost 10; a hash made at a lower cost is replaced at that account's next successful password sign-in, with a write conditional on the old hash so a password changed in between is never overwritten. The upgrade is best effort: a failure is logged and the sign-in still succeeds. While accounts are still on a lower cost, a wrong password for such an account spends the extra comparisons needed to cost as much as one at cost 12, so it takes as long to refuse as an address with no account. An account with no password (one that signs in only through SSO) or with a hash bcrypt cannot read is refused after the same comparison an unknown address gets, so a wrong password does not say which addresses belong to SSO-only accounts (#476). A hash at a higher cost than this one still takes longer to refuse; none is ever written. A password is 8 to 72 **bytes**, the same rule wherever one is set: first-run setup, admin create, admin reset, the account page and signup verification (`user.ValidatePassword`, #368). The upper bound is bcrypt's: it reads only the first 72 bytes and refuses to hash more. It counts bytes, not characters, so accented letters take 2 bytes each and most other non-Latin characters 3 or 4, and a 25-character passphrase in Chinese is already too long. A password over the limit is refused with a 400 that names it (`bad_request`, or `password_too_long` at signup verification). Sign-in applies no maximum, so a hash made elsewhere by a bcrypt that silently truncated still accepts its owner's full password: only the first 72 bytes are compared, as bcrypt always has. A missing or wrong **current** password on `PATCH /me/password` is `400 invalid_current_password`, not 401 (#555): the session is good, and the web client reads every 401 as an expired session and signs the person out. Wrong guesses are throttled per user with a budget the size of sign-in's, counted separately (key `change-password:<user id>`, failures only, same `AUTH_THROTTLE_DELAY` tarpit: the right password always works).
- **Old hashes stop working after N days (#453).** Rehash at sign-in only reaches accounts that sign in, so a hash made below cost 12 is refused too once the grace period ends. `admin.KeyPasswordRehashStartedAt` (`password_rehash_started_at`) holds the moment 1.3.0 first ran against the database, written once by migration 000044 (`ON CONFLICT DO NOTHING`; a fresh install has no old hashes, so its date changes nothing). It is a settings row rather than an `instance_secrets` row: that table is for secrets (a name allowlist and at least 32 bytes), and a date is not one. It is a known key only because the migration test requires every seeded key to be one; `PATCH /admin/settings` refuses it with `readonly_setting`, it is auth-critical, and the settings dump leaves it out, so nobody can restart the clock. N is `password_rehash_grace_days` (Authentication tab, default **30**, 1 to 365 enforced when saved; an unreadable or out-of-range stored value reads as 30). Sign-in only: `user.Service.VerifyPasswordForSignIn` returns `ErrPasswordResetRequired` for a **correct** password on a hash below the current cost once `started_at + N days` has passed, and the handler answers 403 `password_reset_required` (the reset link is sent, or the message says what to do instead; see below). A wrong password still answers 401 `invalid_credentials`, so a guesser learns nothing about the hash. The refusal spends the same bcrypt work as a wrong password on that hash (it burns the cost difference instead of the rehash), pinned by a work-counting test next to #475's. `VerifyPassword` (confirming the current password while changing it) never refuses, since changing the password is the cure. No deadline recorded, or one that cannot be read, means no refusal. **Every administrator is exempt** (rehashed at sign-in as before): administrators are who reset everyone else, setup does not reopen, and email may be off. An earlier version exempted only the last active administrator, which still locked out the others; Erik widened it. Staff and requesters are refused. The SAML restriction (`saml_required`, password sign-in limited to administrators) is checked first, so a non-administrator under SAML gets that refusal rather than a reset instruction. When the server can send email (`passwordResetAvailable()`), the refusal also sends the reset link, through the same code path as "Forgot password" (`requestPasswordReset`, see below), and the message says to check email; Erik's ruling of 2026-10-19, replacing "refuse and point at Forgot password". It is sent only after the password was verified and after the SAML check, so a wrong password or a SAML refusal sends nothing. It spends the same per-address budget as `POST /auth/forgot-password` (`forgotPasswordKey`), and the per-account limit applies when the worker sends. Over the budget, or if the queue insert fails, nothing is queued and the message says "We could not send a reset link just now. Try again in a minute, or ask an administrator." rather than claiming a mail was sent; the code stays `password_reset_required`. The work added to the request is one outbox insert; the mail is sent by the worker. When email is off nothing is sent and the message points at an administrator. The sign-in page shows the "Forgot password?" link in the email-on case (#451).
- Available for all roles by default. With `saml_enabled` on, only administrators keep it (see SAML below).
- **MFA** (optional toggle `mfa_enabled` in admin settings): a second factor is **either** a TOTP authenticator app (Google Authenticator, Authy, etc., enrolled by QR code) **or** a registered passkey (below). Admin can enforce MFA for specific roles (`mfa_enforced_roles`) or all users; an enforced user with no factor is sent to enrolment at sign-in, and right after verifying a self-service signup.
- **A half-signed-in session expires** (#452). After the password is accepted but before the second factor is done, the session is good for **5 minutes** when a code from an existing factor is owed (TOTP or passkey) and **15 minutes** for first-time enrolment, which takes longer (installing an app, scanning a code). The limit is chosen when the session is created (`auth.PendingDeadline`) and stored as `SessionData.PendingUntil`; saving a staged enrolment secret does not extend it. Past it, `SessionAuth` deletes the session row and the request carries no actor, so the route answers its usual 401 and the person signs in with their password again; a refused code is never checked, so it does not use up an MFA attempt. The cookie and row are also given that lifetime so an abandoned sign-in clears itself. A pending session with no `PendingUntil` (saved before this existed) counts as expired, so anyone mid-sign-in at upgrade signs in once more. A fully signed-in session is not affected. Enrolment stays outside `RequireMFA`, so the password is still a way in: the sole administrator can always sign in again and recover (`TestSoleAdministrator_CanSelfRecoverWithNoSecondFactor`).

### Forgot password (#451)

A person who cannot sign in can ask for a link by email and choose a new password. It needs email: with `SMTP_HOST` and `SMTP_FROM` unset, `GET /api/v1/auth/providers` reports `password_reset: false`, the sign-in page shows no "Forgot password?" link, and `POST /auth/forgot-password` answers `503 email_not_configured`. An administrator setting the password directly (`POST /admin/users/{id}/password`) is unchanged and is the route when email is off. It is not "Send reset link" from the user page; that is not built.

- **Ask.** `POST /api/v1/auth/forgot-password` takes `{email}` and **always answers `202`** to a well-formed request (`400` only for a body that is not JSON), with the same body whether or not the address has an account. A string that is not an email address is also answered `202` and queues nothing. The request does the same work for every address: it validates the string and queues **one outbox event** carrying the address, and looks nothing up and writes nothing else, so the timing does not say who has an account either (pinned by a Postgres work-counting test in the style of signup's, #348). Each client address has the `AUTH_RATE_LIMIT_PER_MINUTE` budget (`429`).
- **Decided at send time.** The worker (the `verification` outbox channel, which also carries signup's mail) looks the account up and mails nothing, and does not retry, for an address with no account, a disabled or deleted account, a service account, an account that cannot use a password because `saml_enabled` limits password sign-in to administrators (`user.IsLocalAuthAllowed`, the rule sign-in applies; an administrator keeps it), or an account with **no password** (`password_hash` empty: an identity-provider account, or a local account created without one). **An account is mailed at most once per five minutes**, charged once per request on its first delivery attempt (not per retry), so the endpoint cannot be used to fill an inbox; the earlier link is still good, so nobody is left without one. The budget is in memory and per process, like the guest resend budget. A mail that fails to send is retried by the worker.
- **The link.** `{BASE_URL}/reset-password?token=…`, never built from the request's host. The token is 256 random bits made **when the mail is sent**, so the outbox row holds the address that was typed and no token, and only its **SHA-256 is stored** (`password_reset_tokens`, migration `000049`): a copy of the database holds nothing that can be turned into a reset. **One live link per account**: a newer link replaces the older. **Single use**, and **valid 30 minutes** by the database's clock (`passwordreset.TokenTTL`).
- **Choose.** `POST /api/v1/auth/reset-password` takes `{token, password}`. The password goes through `user.ValidatePassword` first (`400 password_too_short` or `password_too_long`) and a refused password does not use the link up. Then **one SQL statement** spends the link, stores the new hash, clears `must_change_password` and **ends every session the account holds**, so a failure part-way cannot leave a spent link with the old password or a new password with old sessions signed in. A link that is unknown, used, replaced, expired, or for an account disabled, deleted or left with no password since is `422 token_invalid`, one answer for all of them. It works with email switched off afterwards: a mailed link was a promise made while it was on. Counted per client address like the request (`429`), which bounds the bcrypt work an anonymous caller can cause.
- **The link never gives a session.** On success the response is `204` with no cookie. The page says the password is changed and goes back to `/login`, and the person signs in with the new password **and their second factor**, if they have one. A reset link is a credential that sits in an inbox; if it also skipped MFA, a stolen inbox alone would be a stolen account. A test pins that an MFA-enrolled account still answers `mfa_needed` after a reset and that its session cannot read `/me`.
- **Audit.** `entity_type: "user"`, `action: "password_reset_by_email"`, `after: {sessions_revoked: true}`, **no actor** (nobody was signed in, and naming the account would record a claim; the same reasoning as `reset-factors`), distinct from `password_reset_by_admin`. Like that one it is **not** transactional with the write it describes: the password is already changed, a lost entry is logged and swallowed rather than reported as a failed reset, and the write is detached from the caller's context so hanging up cannot suppress it.
- **The #453 refusal.** `403 password_reset_required` (a correct password on a hash below the current cost, after the grace period) has three wordings. Email on and the link queued: it says to check email (and names "Forgot password" for a second link). Email off: it says to ask an administrator and that "Forgot password" is unavailable. Email on but nothing queued (the per-address budget is spent, or the queue insert failed): "We could not send a reset link just now. Try again in a minute, or ask an administrator." This one deliberately does not name "Forgot password", because that endpoint shares the budget and would answer 429 for the rest of the minute. So the first two contain "Forgot password" and the third does not.
- **Not for accounts with no password.** "Forgot password" is for an account that has a password. An account with none (`password_hash` empty) is mailed no link, whether it signs in through OIDC or SAML or is a local account created without a password, and a link that reaches such an account does nothing: the statement that applies a link requires `password_hash <> ''`, so a link made before the account lost its password is refused with `token_invalid` and spent. Giving an identity-provider account a password would put a way in beside the provider's MFA and conditional access, and a local account created without one is left as an administrator made it. The account's password is tested the way the identity provider lockout guard tests it (`password_hash != ""`), not by whether an identity provider subject is set. An identity-provider account signs in through the provider; a local account without a password needs an administrator to set one.

### Passkeys (WebAuthn)

A second factor alongside TOTP (introduced in #302), and later an alternative
to the password itself. TOTP does not change and does not go away; an instance that upgrades
into this notices nothing until somebody registers a key. Passwordless sign-in
is **not built** (see the end of this section): a passkey is asserted after the
password, never instead of it.

**What this claims, and what it does not.** The property bought is
**phishing-resistance**: a WebAuthn credential is bound to the origin it was
registered against, so a look-alike login page cannot use it, which TOTP cannot
claim (a fake page collects the six digits and replays them in the window). That matters on a help desk because a staff account reads every ticket, every attachment and every customer's details. It
deliberately does **not** claim "something you have" in the hardware sense: a
passkey today is very often a *synced* credential (iCloud Keychain, Google
Password Manager), a weaker possession story than a YubiKey. Phishing-resistance
holds for every credential this accepts, synced or not; physical possession
does not, so it is not claimed.

**Storage and the library.** A `webauthn_credentials` table, not columns on
`users`: one person registers several keys on purpose (laptop, phone, a spare
in a drawer). Each row holds the credential id, public key, sign count,
transports, AAGUID, the backup-eligible and backup-state flags, an optional
owner-chosen name (an unnamed key is shown by what its transports and age give,
e.g. "Security key, added 3 March") and created/last-used timestamps.

- `credential_id` has a **unique constraint in the schema**, not a check in Go:
  the specification says ids are globally unique, and a read-then-insert has a
  window whatever it reads.
- `transports` goes back out on the sign-in challenge as
  `allowCredentials[].transports`, so the browser skips authenticators that
  cannot satisfy the request.
- `aaguid` is read by nothing; it is kept because it is free at registration and
  unrecoverable afterwards (the reasoning that keeps the unused CIRCL fields).
- `backup_eligible` / `backup_state` say whether a credential is synced: the
  only way an administrator can tell a hardware key from an iCloud passkey.
  Not captured at registration, the question is unanswerable forever.
- **Sign count is stored and not enforced.** Most modern authenticators return
  zero always and a "must increase" rule locks those people out. A counter that
  was non-zero and goes backwards is a genuine clone signal with no
  false-positive cost; it is logged and gates nothing (the shape of
  `KnownMalicious` in `circl.go`).
- The ceremonies are `github.com/go-webauthn/webauthn`'s; nothing is hand-rolled.
- `last_used_at` is written off the authentication path, in a goroutine with a
  context that does not belong to the request (as webhook dispatch does): it
  answers "which key is still in use", but a sign-in must not wait on it or fail
  because of it.

**Enrolment.** `POST /me/passkeys/register/start` mints a challenge and stages
it **in the session**; `POST /me/passkeys/register/finish` verifies the
attestation and writes the credential. Nothing is written to the account until
the person has proved they hold the key (the reason `GenerateMFASecret` and
`ConfirmMFAEnrollmentWith` replaced `EnrollMFA`, which wrote an unconfirmed
secret over the authenticator its owner still used). A staged challenge is
answerable once and **expires on a fixed deadline from when it was minted**
(five minutes), checked when the assertion comes back, because a challenge left
in a long-lived session is a replay window that stays open as long as the tab
does; it does not slide forward on use, the same rule the MFA lockout follows. Both routes sit inside
`meRouter`'s `DenyMachineCredentials` group (an API key or OAuth client may not
touch how its owner authenticates) and **outside** `RequireMFA`, like TOTP
enrolment, so somebody told to enrol can finish enrolling.

**Finishing registration satisfies this login's MFA challenge, as finishing TOTP
enrolment does:** it sets both `MFAPassed` and `FactorVerified`
(`markPasskeyRegistrationSatisfiesMFA`). Without it a session admitted through
the no-factor-yet branch could register a key and still be refused by
`RequireMFA` until it ran the sign-in ceremony against the key it had just
proved it held ([#307](https://github.com/PubliciaLLC/go-help-desk/issues/307),
item 3). For an already-protected owner registering a replacement it is a no-op.

**Changing factors needs a session that *proved* one, not one that owed none**
([#333](https://github.com/PubliciaLLC/go-help-desk/issues/333)). `MFAPassed` is
true both when a login proved a second factor and when it owed none (MFA off,
optional for the role). The routes that add or replace a factor
(`requireFactorOrFirstEnrolment`, and TOTP enrolment's re-enrol check) read a
separate session fact, `FactorVerified`, set only by:

- a TOTP code (`/auth/local/mfa/verify`) or a passkey sign-in;
- this session finishing a TOTP enrolment or passkey registration;
- an SSO sign-in whose identity provider asserted MFA: OIDC `amr` containing
  `mfa` (RFC 8176), or SAML `authnmethodsreferences` containing
  `http://schemas.microsoft.com/claims/multipleauthn`. Entra ID sends neither by
  default: add the `amr` optional claim to the app registration (for SAML, with
  `include_granular_amr`). Without it SSO sign-in still works, but a user who
  also has a local factor must enter it before changing factors.

Without this, a password-only session on an account where MFA was optional could
replace the owner's factor at any time after they enrolled. A session written
before `FactorVerified` existed reads it as false: such a user signs in again
before changing factors.

**Adding a factor ends every other session,** as a password change does:
finishing TOTP enrolment or passkey registration revokes the account's sessions
and re-issues the current one, so a session opened with the password before the
owner protected the account does not outlive that protection. Enrolment confirm
re-runs the guard rather than trusting the check made when enrolment was staged
([#327](https://github.com/PubliciaLLC/go-help-desk/issues/327)).

**A first TOTP enrolment is a conditional write.** The guard asks "does this
account hold no factor yet?" and the confirm then writes; two confirmations
racing on a fresh account could both pass the question. A session that has not
proved a factor therefore writes with `WHERE NOT mfa_enabled` and the loser is
answered 403; a session that has proved one (a rotation) still overwrites, on
purpose ([#338](https://github.com/PubliciaLLC/go-help-desk/issues/338)).
Passkey registration has no such condition in its write.

**Verifying from the Account page**
([#336](https://github.com/PubliciaLLC/go-help-desk/issues/336)). A session that
owed no factor at login (MFA off, optional for the role, an SSO provider that
asserted nothing, a pre-`FactorVerified` session) is refused on the factor
routes with `403 mfa_required`, and the login page never asked it for anything.
The Account page answers that refusal itself: a code field when the account has
an authenticator app, a passkey button when it has passkeys, posted to the same
`/auth/local/mfa/verify` and `/auth/local/passkey/*` routes the login page uses,
then it retries what the person was doing. No new route. A wrong code or refused
key answers 401 on a live session, so the client does not treat
`invalid_mfa_code` or `assertion_refused` as a lost session.

**Signing in.** `POST /auth/local/passkey/start` and `/finish` sit beside
`/auth/local/mfa/verify`, are refused to machine credentials, and need the
session the password step already produced (`401` otherwise). On a valid
assertion they re-issue the session with `MFAPassed` and `FactorVerified` true.
The gate downstream is `Actor.MFAPassed`, already enforced by `RequireMFA`, so
no route learned a new idea.

**The password step answers with three booleans:** `mfa_needed` (a TOTP
enrolment exists; TOTP wins when the account has both), `passkey_needed` (a
passkey is registered and no TOTP) and `mfa_enrollment_needed` (neither, and the
role is enforced). All false means no factor is owed. Each is also conditional
on the instance setting `mfa_enabled`: with it off, an account that holds TOTP or
passkeys owes nothing at login, and the descriptions above apply with it on. Two booleans keyed off
`u.MFAEnabled`, which is the TOTP column and not "this account has a second
factor", cannot express the passkey-only account: on its second sign-in the
server would say "you still need to enrol" and never "assert your passkey", and
that account could never sign in again. **`MFARequiredFor(role)` is satisfied by
a TOTP enrolment or at least one registered passkey** (refusing the phishing-resistant factor because it is not the older one would be perverse), so `mfa_enabled` and
`mfa_enforced_roles` keep their meanings and an operator reconfigures nothing.

**Losing a key.** The owner removes it from their own account page
(`DELETE /me/passkeys/{id}`, behind the same factor guard) and registers a new
one; that is the ordinary case. An administrator removing ONE credential for
somebody else is **not built**: every passkey route lives under `/me` and
`adminRouter` has none; the user admin page is where it belongs when it is built, and saying it already exists is how an operator ends up looking for a button that was never written. What an administrator can do is the user page's **Reset
MFA**, which clears **every** second factor (the authenticator and all passkeys)
and ends the account's sessions in one statement (`ClearFactors`, all or
nothing; it used to clear only the authenticator, leaving a passkey-only person
locked out by the key they had lost,
[#307](https://github.com/PubliciaLLC/go-help-desk/issues/307) items 2 and 4).
`reset-factors` (below) does the same from the server.

**Self-recovery covers an account with *nothing* enrolled, not a lost key.**
Enrolment lives outside `RequireMFA`, so somebody whose factors were cleared (or
who never had any) signs in with their password, reaches enrolment and recovers
alone; `TestSoleAdministrator_CanSelfRecoverWithNoSecondFactor` pins it. An
account that still *has* a registered factor is different, and both enrolment
doors refuse a session that has not passed MFA: without that refusal somebody
holding only the password registers their own key and obtains the second factor,
passing the gate rather than breaking it. The consequence is a real lockout:
**an administrator whose registered key is lost cannot recover alone**, and a
*sole* administrator has no one to ask (setup does not reopen). The recovery is
`reset-factors` on the server, not an administrator clearing one credential,
which is not built. This is not new: `GenerateMFASecret` has refused
re-enrolment for a TOTP-protected account since the re-enrolment fix.

**The way back in is a command run on the server**, not a recovery code and not
a second factor required up front:

```
/app/server reset-factors <email>
```

It clears the account's TOTP enrolment, removes its registered passkeys and ends
its sessions (one statement), so the next sign-in reaches enrolment and the
person starts again. It is the answer for every cause of lockout, and for the
sole administrator the only one, since the web path must keep refusing (a
password alone being enough to replace somebody's second factor is the bypass
the guards exist to prevent). It takes an email address only and does not touch
the password (`unlock`, below, does). It grants nothing new: anyone
able to run it already has the filesystem and the database credentials, so it
widens no web-facing surface, which a recovery code (another secret at rest,
worth stealing, the property passkeys exist to remove) would.

**`unlock` is the complete version** (#497):

```
/app/server unlock <email>
```

(In the published image the binary is `/app/server`:
`docker compose exec app /app/server unlock <email>`.) In ONE transaction it
does what `reset-factors` does (`ClearFactors`: every second factor and every
session), and also re-enables the account if it was disabled, clears the TOTP
failure count and lock, and sets a **new random password** (24 characters from
`crypto/rand`, hashed at the current bcrypt cost) that it prints **once** to
standard output after the commit, never to the log and never into the audit
entry. It refuses an unknown or deleted address, and if any step fails
(the audit write included) nothing changes and nothing is printed. It does not
touch the in-memory sign-in throttle, which lives in the server process and
only delays a login, never refuses the right password.
It refuses an OAuth client's service account (#490), which has no password to unlock, naming the client.

**The password it prints must be changed at the next sign-in.** It sets
`users.must_change_password` (migration 000045). While the flag is set the
session a correct password opens may do exactly one thing, change the password
(`PATCH /me/password`, which asks for the current password as ever); the
login answer carries `password_change_required` and the login page shows the
change step first. Every other route answers `403 password_change_required`:
`RequireMFA` refuses it for all the routes behind it, and `RequirePasswordCurrent`
for the enrolment routes outside it, the way the MFA-pending gate holds a
half-signed-in session. Changing the password (`SetUserPasswordHash`) clears the
flag. **The flag must never stop an account signing in**, which is the
last-administrator rule applied to a new gate: it gates nothing at login, and
the password change is reachable without having passed MFA. Otherwise an
administrator whose role must enrol MFA would be held at "enrol first" by
`RequireMFA` and at "change the password first" by the flag, with a way out of
neither. That exemption is for a session that owed no factor when the password was
checked (the login decides, into `PasswordChangeNeedsNoFactor`), never for the raw
flag: the flag outlives the cleared factors, and a single sign-on session, which
ignores it, can enrol a new one, after which a password alone must not be enough
to change the password (`TestMustChangePassword_DoesNotLetAPasswordOnlySessionSkipAnEnrolledFactor`).
An account with a factor verifies it first and then changes the password
(`TestMustChangePassword_DoesNotLockOutAnAccountThatOwesEnrolment` covers the
other case).
Single sign-on and passkey sign-ins do not read the flag; it is about the
password, and a federated administrator who never uses it simply keeps it set.

**Its audit entry** (`action: "account_unlock"`, `actor: "server console"`, with
the operating-system user and host and no actor id) is written in the same
transaction, unlike `reset-factors`' best-effort write below: this command
hands out a credential, so an unlock with no record is refused.

**`reset-factors` writes an audit entry** naming the account and the operating-system user
and host that ran it, with no actor id: nobody signed in, and inventing one
would record a claim rather than a fact. The shape (`entity_type: "user"`,
`action: "mfa_reset"`) is the one the user page's Reset MFA writes, and an
administrator resetting somebody's password writes `password_reset_by_admin`
naming themselves as actor. None of this prevents anything (whoever can run the
command holds everything an audit entry could gate), but helping a colleague who
lost a phone is a normal operational event that belongs in the trail with every
other account change
([#306](https://github.com/PubliciaLLC/go-help-desk/issues/306)).

**The last-administrator guard's fourth case belongs with passwordless sign-in.**
The guard (see User Management) refuses to disable, demote or delete the last
active administrator and says nothing about removing their last way to
*authenticate*. That is correct only while a password is always a way in. The
entry points today are local login, SAML and OIDC; a second factor gates a
session that has already authenticated and is not a way in by itself, so removing
somebody's last second factor cannot strand them while a first factor exists, and
there is no operation for a fourth case to refuse yet. What strands somebody today
is a lost registered key (`reset-factors`), and an account provisioned by an
identity provider that has no password when that provider is switched off
([#300](https://github.com/PubliciaLLC/go-help-desk/issues/300); see the guard
below). **When the password stops being a way in, self-recovery stops working**,
and removing an administrator's last credential becomes the same permanent
mistake as deleting the last administrator. The guard grows its fourth case **in
the change that introduces passwordless sign-in**, not afterwards and not as a
follow-up issue, and `TestSoleAdministrator_CanSelfRecoverWithNoSecondFactor`
must be made to pass for passkeys before that change is done. `reset-factors` is
a precondition for that case, not its trigger: "refuse to remove the last way in"
is half an answer without "and here is how you recover when it happens anyway".

**`BASE_URL` is security-relevant.** A WebAuthn credential is bound to its
origin and the relying party is derived from `BASE_URL`, so the setting stops
being only about links in emails. An instance that changes domain invalidates
every registered credential and every user re-registers. A `BASE_URL` that
cannot be a relying-party id is never defaulted, because a default would produce
passkeys that silently never verify.

**An unusable `BASE_URL` turns passkeys off; it does not stop the server** (#494).
go-webauthn accepts a domain name and `localhost`, and rejects an IP address
(`127.0.0.1`, `192.168.1.10`, `[::1]`), a single-label host (`helpdesk`) and a
non-ASCII host (pinned by `TestNewService_WhichHostsTheLibraryAccepts`). Many
1.2.x instances run on exactly such a LAN URL, and a second factor they never
used must not take the whole help desk down. So startup logs once (`passkeys
unavailable: ...` with the reason) and carries on with no passkey service, and:

- `GET /admin/security-warnings` gains `passkeys: {available, reason}` and the
  admin banner (`InsecureConfigBanner`) says `BASE_URL` must be a domain name or
  `localhost`.
- `GET /auth/providers` gains `passkeys` (boolean, public, no reason), so the
  UI offers no key it cannot check. The account page hides enrolment when its
  list request answers `503 passkeys_unavailable`.
- `GET /me/passkeys`, `POST /me/passkeys/register/start|finish` and
  `POST /auth/local/passkey/start|finish` answer `503 passkeys_unavailable`
  (the `requirePasskeys` middleware, so no handler can reach the nil service).
  `DELETE /me/passkeys/{id}` still works: it never uses the service.
- **A passkey-only account is refused, not let in on its password alone.** A
  passkey is only ever a second step after the password (there is no
  passwordless sign-in). While passkeys are off, a key cannot be checked, and
  accepting the password alone would let a stolen password take over exactly
  the accounts that chose a stronger factor. So when the instance's MFA is on
  and an account's only second factor is a passkey, `POST /auth/local/login`
  answers `403 passkey_unavailable` ("Your passkey can't be used on this server
  right now. Ask an administrator to reset your sign-in factors."), creates no
  session and does not count as a failed attempt. An administrator's **Reset
  MFA** (which clears the passkeys too, see above) unblocks the account; it
  then signs in and enrols TOTP as usual. An account with a passkey **and**
  TOTP uses TOTP as always. `passkey_needed` is never sent while passkeys are
  off.
- **There is no exception for the last administrator.** They are refused too,
  and `alreadyProtected` still counts passkeys, so a password alone cannot
  enrol TOTP on any passkey-only account. Recovery is on the server:
  `docker compose exec app /app/server reset-factors <email>` (from the `docker/` directory; clears every
  factor, including passkeys), or an administrator's Reset MFA for anyone else.
  Fixing `BASE_URL` also brings old keys back, provided the domain is the one
  they were registered on.

**Not built**, written down so it is not rediscovered as a gap: passwordless
sign-in; attestation verification against a metadata service; a per-role
"passkey required, TOTP no longer sufficient" policy; and any route by which a
machine credential could register or use a passkey (`DenyMachineCredentials`
refuses that and will keep refusing it).

### SAML (Optional, Off by Default)

- Configured under **Admin → Settings → Authentication** with the IdP's metadata, given as a URL the server fetches or as the metadata XML pasted or uploaded on the settings page (readable after saving: it is public data), and a service-provider certificate and key (the key is write-only). The IdP registers this instance from `GET /api/v1/auth/saml/metadata`. Sign-in starts at `GET /api/v1/auth/saml/login`, which always opens a new request at the IdP (even when the browser still holds a hand-over cookie) and accepts no return address. It returns to `POST /api/v1/auth/saml/acs`, then always to `GET /api/v1/auth/saml/complete` ([#390](https://github.com/PubliciaLLC/go-help-desk/issues/390); pinned by `TestSAMLLogin_RedirectsToTheIdP`, `TestSAMLLogin_IgnoresReturnToParameters` and `TestSAMLLogin_StartsFreshEvenWithAHandoverCookie`). IdP-initiated sign-in is not accepted. The login page shows a **Sign in with SAML** button, a plain link to `GET /api/v1/auth/saml/login`, when `GET /auth/providers` reports SAML enabled (the middleware is loaded, whatever `saml_enabled` says) ([#393](https://github.com/PubliciaLLC/go-help-desk/issues/393), [#421](https://github.com/PubliciaLLC/go-help-desk/issues/421)).
- **SAML runs whenever the IdP metadata (a URL or XML), the certificate and the key are all set**; otherwise its routes answer `503 saml_not_configured`. There is no on/off switch for SAML itself. The settings page only shows the SAML fields while the "Enable SAML login" (`saml_enabled`) toggle is on, so it steers an operator configuring SAML toward also turning off password login for non-administrators; the toggle only takes effect when the settings are saved (the SAML fields have their own "Save SAML config"), so it can be switched back off after the SAML config is saved.
- **IdP metadata: a URL or XML (#461).** `saml_metadata_url` and `saml_metadata_xml` are alternatives. A save that would leave both set is refused: `400 saml_metadata_conflict` on `PUT /admin/saml`, and `400 bad_request` on the settings PATCH. Pasted XML is parsed before anything is saved, by the parser a fetched URL goes through (`samlsp.ParseMetadata`). A document that does not parse, is not SAML metadata, has no `IDPSSODescriptor` (most often this instance's own SP metadata pasted by mistake), or has no signing certificate that parses as X.509 (the certificates the SP verifies every assertion against; such metadata loads, so it would otherwise count as reachable for the lockout guard and then refuse every sign-in) is refused with `invalid_saml_metadata`, and one over 256 KiB with `saml_metadata_too_large`. Go's XML decoder resolves no external entities, fetches no DTD and expands no declared entity, so a pasted document cannot make the server read a file or a URL (pinned by `TestParseIdPMetadataXML_FetchesNoExternalEntity`). The settings page's upload button only fills the text box, so pasted and uploaded XML are the same input. Saved XML is never refreshed: when the IdP rotates its signing certificate, paste its new metadata. A URL is fetched again at each save and each restart. The private key stays write-only; the XML is returned by `GET /admin/saml` and the settings dump.
- **`saml_enabled`** (the "Enable SAML login" toggle) is a separate, stricter posture an operator opts into: it removes password login for non-administrators (`user.IsLocalAuthAllowed`; refused with `403 saml_required`), and `GET /auth/providers` says so with `local_admin_only` (true exactly when `saml_enabled` is on; `local` stays true, since the password form is still offered). `GET /auth/providers` is JSON, and its `saml` and `oidc` entries mean *loaded and able to sign someone in now*, not *switched on*: an identity provider that did not answer no longer gets a button that leads to a 503 (#421).
- **Admin failsafe**: administrators can still sign in with a local password.
- **First sign-in provisioning.** A known SAML subject signs in and has its email and display name refreshed from the assertion (the domain allowlist is not applied again). An unknown subject creates a `User`-role account, provided the email's domain passes `allowed_email_domains` (an empty list is unrestricted for this path). It never adopts an existing local account by email: an address already held by another account is refused (`/login?error=email_taken`). The email comes from `email`, `mail`, the LDAP `mail` OID or Entra ID's `…/claims/emailaddress`, falling back to the NameID. The name comes from `displayName`, Entra ID's `…/identity/claims/displayname`, `cn` or `name`, then the given name (`givenName`, `firstName` or `…/claims/givenname`) and surname (`sn`, `lastName` or `…/claims/surname`), either alone if only one is sent, then Entra ID's `…/claims/name` (ranked last because Entra fills it with the user principal name), falling back to the email (#461; `TestSAMLIdentity`). A blank value counts as absent. Each sign-in of a known subject rewrites the stored name, and the email when it is valid, from these, so an IdP that starts sending a name, or a release that starts reading one, changes the display name at the next sign-in. Other refusals redirect to `/login?error=` with `domain_not_allowed`, `account_disabled`, `account_link_refused`, `invalid_assertion`, `email_not_verified`, `sso_session_used` or `invalid_email` (an address that needs quotation marks, see Guest Submission). The login page shows a fixed message for each of these codes and a generic one for any other; it never displays text from the query string. An SSO sign-in counts as having passed MFA; whether it also counts as having proved a factor is under Passkeys above.

**Supported IdPs:** Okta, Azure AD / Entra ID, Google Workspace (whose metadata is a download: paste or upload it; standard SAML 2.0; additional IdPs should work via metadata import).

The SP root URL passed to the SAML library carries a trailing slash
(`{baseURL}/api/v1/auth/`), not the bare prefix (see `auth.NewSAMLMiddleware`).
Without it the library's relative URL resolution computes `saml/metadata` and
`saml/acs` one path segment short of the routes this server registers, every
request to them 404s, no login completes and no IdP can fetch the metadata, in
any configuration (found while testing #304; pinned by
`TestNewSAMLMiddleware_ComputesRoutesMatchingTheServerMounts`).

The SAML library's own login cookie (`token`, a signed JWT) is a hand-over,
not a session. `/auth/saml/complete` spends it as soon as it has read it,
whatever the outcome. The browser is told to delete it (under the name,
domain and path the library set it with), and the SHA-256 of its signed input
(`header.payload`, never the signature) is recorded in `spent_saml_handovers`.
The signature is left out on purpose: the verifier decodes it leniently, so
one token has several spellings that all verify (unused low bits in the last
base64 character, or the ECDSA `s` value replaced by `N-s`), and keying on the
whole cookie would let a respelled copy through. A cookie whose signed input is
already recorded is refused with `/login?error=sso_session_used`. The app
session written at hand-over is the only credential from then on, so a copy of
the cookie captured earlier cannot mint a new session after a revocation
(password change, MFA reset, a new factor, disable). Without a revocation it
cannot mint one either
([#337](https://github.com/PubliciaLLC/go-help-desk/issues/337); pinned by
`TestSAMLComplete_RefusesASpentLibraryCookie`,
`TestSAMLComplete_RefusesARespelledSpentCookie` and
`TestSAMLComplete_CapturedCookieDoesNotSurviveRevocation`). The primary key
decides, so two concurrent uses of one cookie cannot both succeed. If the
record cannot be written, the sign-in fails with a 500 rather than going
ahead. Rows are purged after a day.

Every hand-over JWT carries a random `jti` (`auth.HandoverCodec`), so two
sign-ins by one person in the same second never share a signed input and
cannot collide in the record.

The cookie and its JWT live five minutes (`auth.SAMLHandoverMaxAge`), not
the library's default of one hour. That bounds the case the record cannot
close: a cookie copied in flight and used before the browser it was issued to
reached `/complete`. Five minutes rather than seconds, because the JWT's
expiry is checked against the clock of whichever instance serves `/complete`.

What this does not cover: the assertion POST to the ACS can still be replayed for about 90 seconds, by someone holding both the POST body and the browser's request-tracking cookie (`saml_` followed by the RelayState: HttpOnly, sent only to the ACS path). IdP-initiated sign-in is off, so the library accepts a response only when its `InResponseTo` matches the request ID inside a tracking cookie sent with it; the body alone is refused (pinned by `TestNewSAMLMiddleware_RefusesIdPInitiatedResponses`). Neither half is single-use. The library keeps no record of assertion IDs, the browser is only asked to delete the tracking cookie, and both are accepted for `MaxIssueDelay` (90 seconds), so a copy of the pair can mint a fresh hand-over cookie in that window. That was true before #337 and is not changed by it.

This is a one-time-use record, not a "sessions revoked at" timestamp checked
against the JWT's issue time. The timestamp would have to be written by
every revocation path (several, including one in SQL that never calls the
session store). It would compare one host's clock with another's at
one-second precision. It would also do nothing against a replay where
nothing was revoked. OIDC has no equivalent cookie: its login state lives in
the server-side session and is cleared on use.

The request-tracking cookie is `SameSite=None; Secure` when the base URL is https, because the IdP returns the browser with a cross-site POST, and a cookie with any other SameSite value would be withheld from it. Over http (development) it carries no SameSite attribute: browsers refuse `None` without `Secure`, and explicit `Lax` would be withheld from the POST. The hand-over cookie (`token`) is `SameSite=Lax`, because it is only read on the top-level GET to `/complete` that the ACS redirects to ([#399](https://github.com/PubliciaLLC/go-help-desk/issues/399); pinned by `TestNewSAMLMiddleware_TrackingCookieIsSameSiteNoneOverHTTPS`, `TestNewSAMLMiddleware_TrackingCookieHasNoSameSiteOverHTTP` and `TestNewSAMLMiddleware_HandoverCookieIsSameSiteLax`). The whole sign-in (login, a signed assertion from a fake IdP, the ACS, `/complete`, and a refused replay) is covered by `TestSAMLSignIn_EndToEndWithASignedAssertion`.

### OIDC (Optional, Off by Default)

- Configured under **Admin → Settings → Authentication** (`PUT /api/v1/admin/oidc`, refused to machine credentials; `GET` blanks the secret): `oidc_enabled`, `oidc_issuer_url`, `oidc_client_id`, `oidc_client_secret` (write-only; a blank secret on save keeps the stored one) and `oidc_redirect_url`. `PUT /admin/oidc` takes no redirect URL: the stored value is kept, or `{BASE_URL}/api/v1/auth/oidc/callback` is filled in when there is none, and that is what the identity provider must allow. The provider is found by OIDC discovery from the issuer URL and reloaded without a restart. Enabling it with a blank issuer, client id or secret is refused (`400`).
- Sign-in is the authorization-code flow with PKCE (S256), a `state` and a nonce, scopes `openid profile email`. `GET /api/v1/auth/oidc/login` redirects to the provider; `GET /api/v1/auth/oidc/callback` finishes it and redirects to `/`. A refused callback redirects to `/login?error=<code>`, as SAML does, so the login page can say why. A missing or mismatched `state` is `invalid_state`, and a missing session is `invalid_session`. The routes answer `503 oidc_not_configured` while no provider is loaded. The login page shows a **Sign in with OIDC** button when `GET /auth/providers` reports it enabled, which means the provider is loaded. `GET /admin/oidc` and `GET /admin/saml` carry two facts: `configured` (the saved settings are complete) and `live` (the provider is loaded now), so "saved, not working" is `configured: true, live: false` (#421).
- **Only a verified email counts.** An address the provider has not marked `email_verified` is dropped: it cannot be used to adopt an account or provision one. A subject already bound to an account still signs in; a first-time login with no verified email is refused (`email_not_verified`), and one whose address needs quotation marks with `invalid_email` (see Guest Submission).
- **`allowed_email_domains` applies to every OIDC sign-in** that carries an email (`domain_not_allowed`), unlike SAML where it gates provisioning only; an empty list is unrestricted.
- **Provisioning and linking.** The `sub` claim is the primary key. A known subject has its email and name refreshed. An unknown subject whose verified email matches an existing local account **adopts** it (matched on the parsed address, so `<a@x>` or `"a"@x` from the provider finds `a@x`), but only when that account is active, is not an administrator, is not already federated through SAML and is not bound to a different OIDC subject (otherwise `account_link_refused`; the write statement asks the rule again, so an account promoted in between is not adopted). An unknown subject with no matching account creates a `User`-role account. A disabled account is refused (`account_disabled`), an address held by another account is `email_taken` and a missing subject is `invalid_id_token`. The name comes from `name`, `preferred_username`, given plus family name, then the email.
- An OIDC sign-in counts as having passed MFA (the identity provider's job); `FactorVerified` is set only when the ID token's `amr` contains `mfa` (Passkeys above).

### Self-Service Signup (Optional, Off by Default)

A visitor creates a `User`-role account for themselves and proves they own the address before it exists.

- Settings, all under **Admin → Settings → Authentication → Registration** and all auth-critical (a machine credential cannot change them): `self_signup_enabled` (off), `allowed_email_domains` (one domain per line) and `open_registration_enabled`. With a non-empty domain list only those domains may sign up. With an empty list, signup is refused (`422 domain_not_allowed`) unless open registration is on, so "anyone with any address" is a separate, confirmed choice and never the accident of a blank list. The same domain list also gates SAML provisioning and OIDC sign-in (above).
- `GET /api/v1/auth/signup/status` (public) returns `{enabled, open_registration, restricted_by_domain_list, saml_enabled}`; the login page offers "Create one" from it. `open_registration` is what is in force: the setting **and** no domain list, because a non-empty list overrides open registration. `restricted_by_domain_list` is true when such a list limits signup, which is why `open_registration` can be false while its setting is on (#421). Who may sign up is unchanged.
- `POST /api/v1/auth/signup` takes `{email}` and answers `403 signup_disabled` when off. The signup form takes only the address: no password (#360) and no display name (#374), and either one sent by an older client is ignored. The address must be a single valid email (`400`), and not one whose local part needs quotation marks (#389, see Guest Submission). Attempts are counted per source address (`AUTH_RATE_LIMIT_PER_MINUTE`, `429`). It stores a **pending registration** (address and a 24-hour token; one pending row per address, and a repeat only re-issues the token, since no name or password is stored) and queues one event naming only that registration's id; the verification mail, a link to `{BASE_URL}/verify-email?token=…`, is sent from the notification outbox, not on the request (see the signup bullet under Notifications). Nothing is sent if SMTP is not configured, so signup cannot complete on an instance without email; turning `self_signup_enabled` on in that state is refused (`400 email_required`, see Notifications), and an instance that already has it on is warned in the admin security warnings.
- **Same answer whether or not the address already has an account.** The response is `202` with the same body either way (deleted accounts still own their address), and a new and a taken address do the same work on the request: the address is checked, the pending row written and one event queued. Whether a mail goes out is decided when it would be sent, off the request: the send re-reads the row and mails nothing if the row is gone or expired or the address has an account, so a taken address gets a pending row that is never mailed and expires unused. Because neither case dials the mail server on the request, the timing does not tell them apart either. That is the request only: the timing of the send is a separate signal, recorded under Guest Submission. Anything that distinguished them would make signup a way to find out who has an account here; the person who forgot they have one reaches for the login page.
- **The display name and password are chosen at verification** (#360, #374). The link opens the `/verify-email` page, which asks for both, neither pre-filled. `POST /api/v1/auth/verify-email` takes `{token, display_name, password}`: `422 token_invalid` for an unknown, used or malformed token, `422 token_expired` past 24 hours, `400 display_name_required` for a blank name, `400 password_too_short` for a password under 8 characters and `400 password_too_long` for one over 72 bytes; all three are refused without using up the link. An address that has gained an account since the link was mailed is also `422 token_invalid`. Anything that is not a verdict on the link, such as a database fault, is `500 internal_error` on the POST and on the lookup below, and the page keeps the form for it (#373). On success it creates the account with that name and password, deletes the pending row and signs the person in. When MFA is on and enforced for the `user` role (`mfa_enforced_roles`) the response says `mfa_enrollment_needed`, the session does not count as having passed MFA yet, and the page shows the same enrolment form a password login does before it goes to the dashboard (#369). The page first looks the link up with `GET /api/v1/auth/verify-email?token=` (#370), which answers `{email}` and nothing else and refuses an unknown, used, malformed or expired link the same way the POST does (`422 token_invalid` or `token_expired`), and so an address that has gained an account since the link was mailed (`422 token_invalid`, #421; before that the lookup answered 200 and only the POST refused). This reopens nothing from #348: `POST /auth/signup` is untouched, and a token for a taken address was never mailed, so its only holder is the inbox owner who received the link before the account existed. A link can still fail at the POST if the account appears between the two calls. That lets it show which address the account is for, hand a password manager the address, and say a dead link is dead before anything is typed. It never returns a display name, because none is stored before verification. Choosing the name and password there means only whoever holds the link, that is the owner of the mailbox, sets them: when signup carried them, anyone who knew an address could sign up after its owner and have the owner's newest link create an account with the other person's password, and later with their name (an attacker's words, on this site, in front of the inbox's owner).

### Identity provider lockout guard (#300)

A federated account (SAML or OIDC) can have no local password at all
(`password_hash` empty; local login refuses it with 401), so its only way in is
that provider. Disabling or clearing the provider's configuration in **Admin →
Settings** removes that channel while leaving the row, the role and the
active-administrator count untouched: the same class of mistake as the
last-administrator guard (see User Management), reached through a door that
guard does not watch, since it watches the administrator *row* and not their
ability to authenticate.

Saving the OIDC or SAML configuration therefore computes what each provider's
reachability will be immediately afterwards and looks at every active
administrator:

- If the change would leave **every** active administrator with no way to
  authenticate, the save is refused (`400`), the same severity as `ErrLastAdmin`
  and for the same reason: this is the unrecoverable case.
- If it strands **some** administrators but at least one other can still sign in
  and fix things, the save is allowed and a warning names who is affected.
  Refusing would only move the unrecoverable-lockout shape onto somebody else's
  account, and an operator migrating providers deliberately should not be blocked
  by a stranding they already know about.
- **Viability.** A password is always a viable channel, independent of either
  provider's state. A federated subject is viable only while its **own** provider
  is reachable: an OIDC subject is not a channel through SAML, and vice versa.
  MFA (TOTP or passkey) is deliberately not consulted: a second factor is never a
  way *in* on its own, so it cannot rescue a stranded administrator and cannot
  strand one.
- SAML reachability depends only on the IdP metadata (a URL or XML), the
  certificate and the key being non-empty plus a successful metadata load (a
  fetch for a URL, a parse for XML). The guard gives SAML **no `enabled`
  flag**, and `saml_enabled` is not part of the load decision: it already means
  something else (it removes password login for non-admins). Wiring it in would
  take SAML down on every instance that leaves it off to keep password login
  alongside SAML, and backfilling it to true to avoid that would silently remove
  password login from every non-administrator there (tried and reverted before
  merge in #304).

**How "reachable" is decided differs by route, on purpose.**

- The dedicated endpoints (`PUT /admin/oidc`, `PUT /admin/saml`) build the real
  provider or middleware against the *candidate* configuration (running OIDC
  discovery, or fetching or parsing the SAML metadata) **before** persisting
  anything, and the guard's decision is that real outcome. "Are the fields
  non-empty" is wrong in the dangerous direction: a well-formed but unreachable
  IdP (a typo'd issuer, a metadata endpoint that is down) would sail through. The
  already-built object is what gets committed on success, so the guard's decision
  and the live effect agree.
- The generic `PATCH /admin/settings` reaches the same keys but does not
  live-reload either provider, so there is no construction attempt to observe. It
  falls back to the field-completeness check: narrower, but real coverage for a
  route that otherwise could set `oidc_enabled: false` or blank a SAML field with
  no refusal and no warning.
- `reachable` is unconditionally `false` whenever the *candidate* fails to build
  (both providers), whatever the currently live provider says;
  `buildOIDCProvider` and `buildSAMLMiddleware` return it separately from
  `commit`. Two different questions, and only one survives a restart: the live process keeps its
  fail-safe (a bad edit or a transient IdP outage leaves what is running
  untouched), but the guard reasons about what the persisted row would do on a
  cold load.
- An enabled-but-incomplete OIDC configuration (blank issuer, client id or
  secret) is refused outright (`400`) through either path rather than
  reachability-checked: at the next restart there would be no OIDC at all, and a
  save that only looked safe because an old, unrelated config was still live is
  exactly the gap this guard closes. SAML has no equivalent case; any blank field
  is unreachable unconditionally.
- The generic PATCH (`ssoSettingsWarning`) refuses outright (`400`) a JSON type mismatch (`oidc_enabled`
  sent as the string `"false"`) **and the literal `null`** (`unmarshalSetting`),
  for every key the guard reads. The write that follows persists the malformed
  value regardless, and every real reader (`GetBool`/`GetString`) silently
  returns the Go zero value, flipping the setting to disabled/blank. A guard
  reasoning about one value while the system reads another from the same bytes is
  worse than none, because it reports confidence it does not have. (`null` is
  caught separately because `encoding/json` treats it as a no-op rather than an
  error when decoding into a non-pointer.)

**A narrow race is accepted.** The guard reads the active-administrator list,
decides, and only then writes the setting, unlike the last-administrator
guard's statements, which decide and write in one `UPDATE`. A concurrent
role change or second settings save can slip between; this is a deliberate,
infrequent action from the settings page, not a path an unauthenticated attacker
can drive, and closing it would need a transaction spanning the users and
settings tables.

**The complete way back is `/app/server unlock <email>`** (#497, #300's option
4), so a locked-out federated administrator has more than a warning that would
have stopped them getting here: it sets a password. See Passkeys, "The way back
in", for what it does. `reset-factors` remains and clears factors only.

---

## Submission, Attachments and Small Screens

### Guest Submission (Optional, Off by Default)

A visitor with no account files a ticket and is sent a per-ticket link. The link
is the whole credential, so it is treated like one: stored as a hash, replaced
whenever the ticket changes in a way the guest is told about, **read-only once
the ticket closes**, and expiring after thirty days if nothing happens at all.

**An address whose local part needs quotation marks is refused** with `400`
(#389), here and everywhere else an address is taken. Parsing drops the
quotes, so `"john doe"@example.com` became `john doe@example.com`, which the
mailer then could not send to: the guest was never sent their link. One with
a leading space, `" a"@example.com`, was worse and was mailed to
`a@example.com`. Quotes that were never needed are dropped and the address
is kept: `"john"@example.com` is stored as `john@example.com`. Addresses
already stored are not changed. A first-time single sign-on with such an
address creates no account: both SAML and OIDC redirect to `/login?error=invalid_email`. An
existing single sign-on user keeps their stored address when the identity
provider sends one that needs quotes; their other details still sync and
sign-in succeeds.

**Closed means read-only, not revoked** (#349). Closing, a status change to
Closed and the auto-close sweep **stop rotating** the guest's link; they do not
delete it. The last link sent keeps reading the ticket until it expires
(`GuestTokenTTL`, thirty days). Before this, closing revoked every link, so a
guest could never read the answer on a ticket that staff replied to and closed
straight away. Reading and writing are different lookups:

- `GET /api/v1/guest/ticket` resolves the token for a **read**, closed or not.
- `POST /guest/replies` and `POST /guest/attachments` (every route that changes
  something, grouped behind one middleware so a new one cannot forget it) resolve
  through the **write** lookup, which refuses a Closed ticket with the same
  error as a link that never existed. The response is the generic `404`,
  byte-identical to the one a bad link gets (status, content type and body), so
  the refusal does not reveal that the ticket exists. A request that races the
  close past the middleware is mapped to the same `404`, not the `409` a
  signed-in reporter gets: see "Races" below.

The replacement link is created when the email carrying it is **sent**, not
inside the transaction of the change (#164). Notifications are queued in an
outbox (below), and the outbox must never hold a working credential: the token
table keeps only hashes. So the change marks its event for a guest link, and
the send re-reads the ticket and rotates. The previous link therefore dies at
the send rather than the commit, normally seconds later. A ticket that has
**closed since** is still sent its mail (#349 replaced #346's skip), with a link
**added** beside the existing ones rather than a rotation; see below.

A link re-request (`POST /api/v1/guest/resend`) does no lookup on the request
at all. Every request, matching a ticket or not, queues the same event carrying
what the visitor typed, and matching, the per-ticket budget and the rotation
all happen at send time. The request used to dial the mail server on a match
and run one query on a miss, so its timing said whether a tracking number and
an address went together, though its answer did not.

**What is left of that signal.** Each server process has one worker sending
rows in order, so a matched resend holds it for a mail-server round trip and
a miss for one query. Someone who queues a probe and then a notification of
their own (a resend for a ticket they hold) could time their own mail's
arrival to learn which the probe was. It needs their own mailbox, crosses a
queue shared with every other notification, and is far noisier than timing the
request was. Recorded rather than claimed closed.

**Signup verification leaves the same signal** (#361). The request side of
signup is covered under Self-Service Signup. Verification rows share
that worker and its order, so one for an address with no account holds it for
a mail-server round trip and one for a taken address for the two queries that
decide not to mail. Someone who signs up for an address and then at once for
their own could time their own verification mail's arrival to learn whether
the first address has an account. Accepted for the same reasons as above: it
needs their own mailbox, crosses a queue shared with every other notification,
and is tens to hundreds of milliseconds against seconds of mail-delivery
jitter. Recorded rather than claimed closed.

The rotation runs in one transaction with the ticket row locked, so concurrent
sends for one ticket (two replicas, or a reclaimed row beside a fresh one)
leave exactly one working link. The per-ticket resend budget is charged on a
row's first delivery attempt only, so a resend whose first send fails is
retried rather than refused. The exception: a row whose first claim never
reached the budget (the worker stopped mid-batch, or the lookup failed) comes
back on a later attempt uncharged, at the cost of one extra rotation.

**A guest email for a ticket that closed before it was sent is still sent**
(#349). Staff reply, then close straight away (common, and what an MCP agent
does): the reply's mail goes out after the close and carries a link that opens
the thread. #346 skipped it and, before the outbox, the same sequence sent
"there is a new reply" with a link that was already dead, so the guest could
never read the last answer.

What "the existing link" means is decided here, because raw tokens are stored
hashed and cannot be re-sent:

- On a **Closed** ticket the send issues a **new** token and **deletes
  nothing**: the links the guest already holds keep working until they expire.
  This is not a rotation. It is added at most once per mail sent on that ticket,
  each expiring thirty days after it is issued.
- If **no** link is live — the thirty days passed, or the ticket never had one —
  the same happens: the mail carries a new link. A closed ticket is never left
  with a mail and no way to read it; staff wrote to the guest, and the mail is
  how they learn.
- A link issued for a closed ticket **can only read**: the write lookup refuses
  a Closed ticket whatever the link's age. Closed is terminal by default; if an
  operator enables forced reopen (`closed_reopen_policy`) and a staff member
  reopens the ticket, the guest's links write again as soon as it is not Closed
  (they belong to the same guest), and the reopen's own mail rotates them all
  into one fresh link, as any reopen does. If that mail cannot be sent, the
  older links stay live until they expire.
- On an open ticket, rotation is unchanged.

**Resend for a closed ticket re-sends a read-only link** (decision, #349). A
guest who has lost their link may ask for another with the tracking number and
address, for a closed ticket as for an open one: both halves must match, the
per-ticket budget applies, and the link is added beside the existing ones, not
rotated, so a resend cannot lock them out. It stays indistinguishable between a
match and a miss: the request still does no lookup and queues one identical
event, so its timing and its `202` say nothing, and only the send-time step
decides. The reason: closing no longer revokes anything, so there is no access
for a resend to "resurrect", and refusing it would leave a guest with a deleted
mail unable to read their own archive for up to thirty days. The cost: each
resend extends that guest's read access by thirty days from the send, bounded by
possession of the mailbox, which is the credential already. (Anyone who knows a
tracking number and an address can still only cause mail to that address; they
never receive the token.)

Submission is a separate public route rather than a relaxation of the ticket
router. Every route under `/tickets/{id}` would otherwise have to re-derive
whether the caller is a guest, which is the shape of the authorisation bug fixed
in 1.2.0.

- **Toggle in admin settings:** when off, all guest routes (both public submission and token-based read/reply/attach) answer 404 to visitors, suspending any links already issued until the setting is turned back on (#424).
- **Guests attaching files** (#445) is a second, separate switch, **Guests may attach files** (`guest_attachments_enabled`, **Admin → Settings → General → Submissions**, under guest submission). **Off by default, upgraded installs included**: before it existed a guest could upload through `POST /guest/attachments` whenever guest submission was on, and that is now closed until an administrator opens it. It is **dependent** on guest submission: the setting only takes effect while `guest_submission_enabled` is on (the settings page shows it unavailable while submission is off, and the server treats it as off), so there is no state in which uploads are on and submission is off.
  - **Size.** `guest_attachment_max_mb` is a per-file maximum for guests, set with a slider from 1 to 100 MB, 5 MB by default. The server holds the guest to the **lower** of it and the instance-wide attachment maximum (25 MB, the one every upload has), so a figure above 25 is accepted by the setting and cannot be reached; the slider says so. A value outside 1 to 100 is refused at save (`400 bad_request`) and not stored, and an unreadable stored value reads as 5. Over the limit is `413 too_large`, with the limit that applied in the message ("file exceeds 5 MB limit"); the body is capped while it is read, as for every upload.
  - **Per-ticket cap and hourly rate** (Erik, 2026-10-19). `guest_attachment_max_per_ticket` is the most files guests may attach to one ticket (1 to 50, default 5); `guest_attachment_uploads_per_hour` is how many uploads one guest link may make per hour (1 to 30, default 3). Both are set under the uploads switch in Settings, unavailable while it is off, session-only, validated at save (`400`, nothing stored) and read as their defaults when unset or damaged, so an upgraded install gets 5 and 3 as soon as an administrator turns uploads on. Both are checked on the guest upload route after the setting, the token and the lifecycle check, the cap first: a full ticket answers `409 guest_attachment_limit` ("This ticket already has the most files a guest can attach"), and the cap does not spend the hour. Past the hourly figure the answer is `429 rate_limited` with `Retry-After` in whole seconds. The rate is keyed by the **ticket the link names**, not the address, so a guest behind a shared address cannot use up another's hour; it reuses `middleware.RateLimiter` (extended with `TakeWithLimit`, because the figure is an admin setting that changes while the server runs, and with an injectable clock for tests). It is a fixed window over the whole limiter, in memory and per process like the other limiters: a restart clears it and each replica has its own. The unit is spent before the work, so parallel uploads cannot all pass a budget with one slot left, and **given back only when the refusal was decided before the scanner**: a wrong type, an over-size file, a name or request the parser refuses (`unsupported_type`, `too_large`, `invalid_filename`, `bad_request`). Those cost the server a body read and nothing more. Everything decided after the scan keeps its unit: `invalid_image`, `invalid_file` (a content mismatch), a malware verdict (`422 infected`), a scanner or storage fault (`503`) and any 5xx, and so does the cap refusal that comes from inside the transaction. Refunding a post-scan refusal let a guest make the server scan without limit (measured: twenty bad PNGs at a budget of one were twenty scans). Because a pre-scan refusal is free of the hour, **every refused attempt also draws on a second, fixed budget of 20 per hour per ticket** (`guestRefusalLimiter`, not a setting), whether or not its unit came back; it is charged up front in one locked step (so parallel refusals cannot overshoot it) and given back when the upload is accepted, and it is charged before the hourly budget, so an attempt turned away by it never spends an hourly upload (#559). Once spent, the route answers `429 rate_limited` with `Retry-After` before reading anything, a good file included, until the window ends. Like the other limiter it is in memory and per process. The cap counts **only guest uploads**, for which `attachments.uploaded_by_guest` (migration 000054) records the fact, because nothing recorded who uploaded a file; staff and reporter files never count. Files a guest attached before the migration are not counted (the column defaults to false). The cap is **exact**: the authority is `ticket.Service.CreateGuestAttachment`, which counts inside the transaction that already locks the ticket row, so uploads racing for the last slot leave exactly one file there (`TestCreateGuestAttachment_ParallelUploadsCannotOvershootTheCap`). A cheaper count before the file is read and scanned only saves that work. `GET /site` carries the cap and the hourly figure with the size, and the page offers at most the lower of what the ticket has room for and the hour allows, worded "up to 3 files now (5 per ticket)" when they differ.
  - **Refusal.** With uploads off the route answers the generic `404 not_found`, byte-identical to a bad link and to guest submission being off, like every other guest refusal: a different status would tell an anonymous caller which setting is in force.
  - **Session only.** All four keys are in `AuthCriticalKeys`: they widen what anonymous people can do, and a leaked API key must not be able to turn them on and use them. They are in `KnownKeys`.
  - **One migration.** 000054 adds `attachments.uploaded_by_guest BOOLEAN NOT NULL DEFAULT false`, so the cap can count only guest uploads (nothing recorded who uploaded a file). The settings themselves are rows read with defaults and need none; files guests attached before the upgrade are not counted.
  - **Where the control is.** The guest ticket page (`/g`) shows **Add files** on an open ticket when `GET /site` says `guest_attachments_enabled`, with the effective per-file limit in `guest_attachment_max_bytes` (the lower of the two figures, so the page never promises more than the server takes). Files go up one after another as they are chosen, to `POST /guest/attachments` with the `Authorization: Guest` header; there is no draft, because a guest's attachment belongs to the ticket and not to a reply. The server's own reason is shown for a refusal. There is no control on the submission page (`/submit`): the link that identifies the ticket is emailed after the ticket exists, so a guest attaches from that link. The page does not list the guest's uploads (the guest read has no attachment list) and renders none of their content; attachments stay download-only.
- Unauthenticated users submit a ticket at `/submit` and receive a **tracking number**
- Guest ticket form collects: **name** (required), **email** (required), **phone** (optional), subject, description, and category (active only — no type or item)
- The tracking number can be referenced when following up with the help desk by phone or email
- No account creation required

### Small screens

Two jobs are supported on a phone: **a staff member triaging away from their
desk**, and **a reporter filing a request and following it**. Administration is
not a job the product is designed around: an operator configuring categories,
editing roles or managing API keys is at a desk, and the admin tables are built
for one. The admin pages do still **fit** a phone: no sideways scroll at 320
pixels, whatever the data (#522). They are not redesigned for it. A wide admin
table stays a table and scrolls inside its own box; the page never does.

**Server warnings.** Administrators see problems with the server (unwritable
attachment storage, an unreachable scanner, passkeys unavailable, email not set
up, and the rest of `GET /admin/security-warnings`) in **one slim bar** above
the page, never a stack of banners. It is a single line: "N server problems
need attention", or the notice's own title when there is exactly one, with a
**Details** button (`aria-expanded`) that opens the full explanation of each
notice. Red marks something broken now (unwritable attachment storage, a
scanner that is down, under either policy: `required` refuses uploads and
`permissive` stores files unscanned right now; insecure secrets; email not being
delivered); configuration notices are amber. An `info` severity (neutral blue)
exists for good news such as an available update: it never turns the bar red,
is not counted as a problem, and is announced politely as a status. **Hide** removes one notice
for the current session: it is kept in `sessionStorage` by notice id, severity
and a short summary of what it said (so a notice that gets worse, such as a
changed scanner policy, more failed email, another blind OAuth client or a
second exposed secret, comes back), sent nowhere, cleared at sign-out and when
the session expires (the 401 handler), and the notice returns at the next
sign-in while the problem remains. When every notice is hidden the bar goes away. The query
is refetched when the window regains focus and when Details is opened, so a
problem that was fixed stops showing within seconds. Everything inside wraps
anywhere, including the `reset-factors` command, because the bar sits above
`<main>` and a long unbroken token there once widened the whole page. Only
administrators are asked. Only the summary line is a live region; the bar
is a labelled region, focus moves to the next Hide button (or Details) after a
Hide, and Hide is at least 44px on a touch screen.

**What each job covers.** The staff path is the queue, a ticket, and the
actions taken on it: read, reply, reassign, change status, resolve. The
reporter path is the new-ticket form, their own list, and the thread they can
read and reply to. Both include signing in.

**How it is built.** The unauthenticated pages (sign-in, registration, first-run
setup, email verification, guest submission, the guest ticket view and the
tracking-number form) render outside the application shell as centred cards with
their own maximum widths, and fit at phone size. Inside the shell, the sidebar
is permanent from the `md` breakpoint up; below it the sidebar becomes a drawer
opened from a top bar, because a fixed 240-pixel column is more than half of a
390-pixel viewport and left the pages beneath it too narrow to lay anything out
in. The ticket list is a table from `md` up and one card per ticket below it.

**The rule for the pages that are in scope** (Dashboard, ticket list, ticket
detail, new ticket): no horizontal scrolling at 390 CSS pixels, controls large
enough to hit with a thumb, and a layout that stacks rather than shrinks. A
five-column table squeezed into a phone is not a mobile layout; where a table
carries one row per thing, that becomes one card per thing. The end-to-end
check `frontend/e2e/mobile-layout.spec.ts` pins it at 390×844.

**Long text wraps; ticket content is never cut.** A token with no break point,
such as a 300-character URL in a reply, is wrapped at the edge of its box
(`overflow-wrap: anywhere`, Tailwind `wrap-anywhere`) down to 320 pixels. This
holds for everything a person wrote on a ticket: the subject heading, the
description, replies and internal notes, reply author names, attachment
filenames, the requester line, the activity feed and its diffs, the status
history, followers, the assignee, status names, category, type and item names,
custom-field labels and values, tags, and the requester picker. Plain-format text
and the other fields listed here are never truncated, rewritten or linkified, so
they stay whole and copyable (#479). Markdown-format text is linked as described
under *Ticket text formatting*; its code blocks and tables scroll inside their
own box, which does not count as cutting text.

**Admin pages (#522).** The same treatment, applied to every page under Admin
(Users and a user's page, Groups, Roles, Categories, Statuses, Tags, Canned
responses, Custom fields, API keys, OAuth clients, Webhooks, Routing, Audit and
each Settings tab): `wrap-anywhere` on what an administrator typed (names,
descriptions, URLs, email addresses), and `min-w-0` on flex and grid children so
they can shrink to it. The tables on Users, Roles, Statuses, Tags, Canned
responses, Custom fields, API keys, OAuth clients, Webhooks and Audit, and the
Categories tree, each sit in an `overflow-x-auto` box. Those tables are still
wider than a phone with short data, so they scroll sideways inside that box and
every column, action buttons included, can be reached; the page does not move.
(They used to be `overflow-hidden`, which cut the columns off instead.) Groups,
a user's page, Routing and Settings are cards and forms, not tables. On Settings, the tab bar scrolls inside its own row, and a
setting's control sits under its label below `sm` instead of beside it, because
a select or number box that cannot shrink does not fit beside a label at 320
pixels. The end-to-end check pins every admin page, with a 300-character name,
description and URL and again with short data, at 320 and 375 pixels, and also
opens the forms and expanded rows on Users, Groups, Categories and Webhooks. It
fails if the page or `main` scrolls sideways, and also if anything sticks out
past `main`'s right edge without an `overflow-x: auto` box around it, which is
what an `overflow-hidden` wrapper looks like.

Fourteen compact places still end in an ellipsis, because they are one-line rows or
chips where wrapping would break the row, and none of them is ticket content:

- in the Linked tickets panel, the linked ticket line;
- in the desktop ticket table, the subject;
- in the reply composer, a pending upload's filename before it is sent, and the
  canned-response preview in its picker;
- in the shell, the signed-in email at the foot of the sidebar and the brand
  name in the mobile top bar;
- on the Dashboard, each client's name and email in the By Client cards;
- on the Account page, a passkey's label;
- in the authenticator-app setup, the secret under the QR code;
- on a ticket, each attachment's SHA-256, cut to its first 12 characters;
- on the Canned responses admin page, the body preview, cut to 80 characters;
- on the Settings Authentication tab, the SAML certificate: its first line, with
  the whole PEM in the `title`;
- on the same tab, the SAML private key: its first line followed by "…". It is
  not cut by CSS, the text is simply shortened, and it has no `title`, on
  purpose: its full text is a secret and must not sit in an attribute.

Each, except the private key line, carries a `title` attribute with its full
text, which a pointer shows on hover; touch browsers do not show one, so the two
ticket rows also open the ticket, where the text wraps in full. Everything else wraps, including the
sidebar brand in the drawer and the Dashboard's "Welcome back" line. On the
ticket page below `md` the header's action buttons sit on their own row under
the title and tracking number, which keep the full width (#480).

**Not a claim of feature parity.** Everything a staff member can do to a ticket
from a desk they can do from a phone, because those actions live on the ticket
page. Bulk selection across a queue is the exception and stays desktop-only: it
is a multi-select over a table, which is the shape that does not translate.

Tracked as [#296](https://github.com/PubliciaLLC/go-help-desk/issues/296); shipped in #299.

### Ticket text formatting

Markdown in ticket descriptions, replies and internal notes ([#443](https://github.com/PubliciaLLC/go-help-desk/issues/443)).

**The format is stored.** Every description and every reply carries `plain` or
`markdown` (`tickets.description_format`, `ticket_replies.body_format`,
migration 000055). Nothing is guessed from the text. Every record written before
1.3.0 is `plain`: 1.2 promised Markdown but never rendered it, so older text
stays as it was typed. The columns have no default after the migration, so a
write path that forgets the format fails instead of choosing one.

| Writer | No `format` sent | Accepts |
|---|---|---|
| REST `POST /tickets`, `/tickets/{id}/replies`, `/guest/tickets`, `/guest/replies` | `plain`, so existing integrations are unchanged | `plain`, `markdown` |
| The web app (every form with the editor, staff, requester and guest) | always sends `markdown` | |
| MCP `create_ticket`, `add_reply` | `markdown` (the tool descriptions say so, and say to escape characters meant literally) | `plain`, `markdown` |
| Follow-ups | copy the original's format with its text | |

Anything else (an explicit `""`, `"Markdown"`, `"html"`, a number) is `400
invalid_format` on REST and a tool error on MCP, and nothing is written. Both
fields are on every ticket and reply object, on the requester and guest views
too. Notes and resolution notes, canned responses and settings are not
descriptions or replies: they stay plain text.

**The renderer cannot output script, raw HTML, images or anything outside one
list.** The guarantee is how the page is built, not filtering. Every element is
created by React from our own component map; all user text is a React text node;
the only attribute that carries user data is `href`, checked against `http`,
`https` and `mailto`. `rehype-sanitize` runs last as a second, independent
check, and the content security policy is a third line of defence, not the
guarantee. `dangerouslySetInnerHTML` and `rehype-raw` are banned by lint.

- **One allow-list** (`frontend/src/lib/markdownAllowList.ts`) drives the
  sanitize schema, the renderer's elements and the editor's node and mark list;
  a test fails when the three disagree. Allowed: paragraphs, quotes, lists
  (numbered lists keep `start`), code blocks, rules, tables, emphasis, strong,
  strike-through, inline code, line breaks, links. All six heading levels render
  as one small bold line, so a requester cannot post a page-sized banner.
  Not allowed: images, task-list checkboxes, footnotes, `style`, `id`, `on*`.
- **Raw HTML is shown as text**, so `<server01>` and `<b>x</b>` read as written.
- **Images are never loaded.** An image shows as the link "[image: alt]", which
  follows the link rules (no tracking pixels, no `data:` images).
- **Links** open in a new tab with `rel="noopener noreferrer nofollow ugc"`.
  The real host is shown after the link unless the link text is exactly the URL
  (plain ASCII, no `user:password@`); a look-alike international domain shows in
  its `xn--` form, and invisible and direction-changing characters are removed
  from link text and the host. Bare URLs and `www.` addresses are linked, but
  only in Markdown text. Relative links, `#fragment`, `javascript:`, `data:`,
  `vbscript:` and `tel:` render as plain text.
- **Code** in a fenced block is highlighted for 13 languages (bash, powershell,
  json, yaml, xml/html, sql, javascript, typescript, python, go, ini, diff,
  plaintext) by `lowlight`, which yields a tree of elements, never an HTML
  string. A fence in another language, or with none, is not guessed at.
- **Formatting is bounded, not guessed.** Parsing Markdown is not linear in the
  size of the input (a table header's width multiplies every short row, unclosed
  brackets rescan the rest of the text, hard breaks and nested lists add up), and
  a reply has no length cap. So text over 50,000 characters is never formatted,
  a table is limited to 5,000 cells, and everything else is parsed in a Web Worker
  with a one-second budget; past it the worker is terminated. In every such case
  the post is shown as plain text with a note ("too long to format", "too
  complex to format", or "formatting unavailable" when the worker could not
  start), and its **Plain text** button stays reachable. A failure that may be a
  slow moment is not remembered, so the next view tries again.
- **Replies are limited to 50,000 bytes, and older longer posts are collapsed.**
  A reply (REST, guest, MCP, anything that makes one) is held to the
  description's limit, counted the same way: **bytes** of the stored text, so
  letters outside basic English and emoji count 2 to 4 each (16,667 Chinese
  characters are 50,001 bytes and are refused). Over it is `400 body_too_long`
  ("body must be at most 50000 bytes"), checked before anything is written. The
  forms say "size limit", never "characters": "This is N% over the size limit
  (50,000 bytes). Letters outside basic English and emoji take 2 to 4 bytes
  each." A reply had no cap before 1.3.0, so some stored posts are longer, up to a
  megabyte. Laying out a megabyte takes seconds, and splitting it into blocks
  changes what copying gives back, so such a post (the page's rule is over 50,000
  characters, a threshold of its own) is shown **collapsed**: its first 5,000
  characters, a note ("Showing the first 5,000 of N characters. This is only part
  of the post: copying here copies only that part.", counting real characters, an
  emoji as one), and a **Show full text (size)** button. The button draws all of
  it in one block, as stored, so selecting and copying it returns exactly the
  stored text (tested in Chromium and WebKit), with one exception that is the
  browser's and not ours: a browser drops a trailing newline when copying, for
  every post, formatted or plain, collapsed or not. Saving does not change it.
  Expanding is allowed to be slow, once, and only when asked for. Nothing is
  truncated or rewritten: the rest is one click away. Over 50,000 characters is
  never formatted, expanded or not.
- **A page of long posts is formatted only where it is read.** A post is formatted
  when it comes within about 1,500 px of the screen (measured in the scrolling
  area that holds it), and the page as a whole has a budget of 300,000 formatted
  nodes; past it, further posts stay as written with the note "this page already
  shows a lot of formatted text". Without this, 220 replies of 50 KB each took
  3.76 GB of heap. The page
  itself only turns the worker's sanitized result into elements from the
  component map, after checking it again, inside an error boundary per post. The
  worker is same-origin, so `script-src 'self'` covers it (pinned by
  `TestDefaultCSP_AllowsTheSameOriginMarkdownWorker` and `frontend/e2e-csp`).
- **Two kinds of link are not links.** A `mailto:` with any query (`?bcc=`,
  `?cc=`, `?subject=`, `?body=` can add recipients or write the message), with a
  `;`, a quote, a backslash, whitespace or control character, an `@` or a `%` left
  in a local part, or a domain that is not a host; its addresses are otherwise
  shown with the domain in `xn--` form. And a link to one of this help desk's own
  attachment routes, which would skip the "content looks like..." and reputation
  warnings of the attachments list: it reads "(attachment: open it from the
  attachments list)". That rule compares what the server compares: the host
  without scheme, port, case or trailing dot, and the path with repeated slashes
  collapsed, decoded once and lower-cased.
- **Long text** (#479, #517): prose and links wrap; a code block or table scrolls
  inside its own box (focusable by keyboard); the page itself never scrolls
  sideways at 320 px.

**Reader toggles.** Every Markdown post has a **Plain text** button that shows the
stored characters, to copy them exactly or check a suspicious link. Every plain
post has **Show formatted**, which shows the same text through the same strict
renderer. Both are the reader's choice, change nothing stored and send nothing to
the server; the default is the stored format. Both buttons use `aria-pressed`.

**The editor.** The web app's forms use a Milkdown WYSIWYG editor that writes
Markdown, loaded on demand; it is built from the same allow-list (no task lists,
footnotes or images; an image node is the text "[image: alt]"). A paste is parsed
against that schema, so a pasted image or script is dropped and nothing is
requested from the network. Tab always leaves the editor (list indent is
`Mod-]`). While it loads, if it fails, or by the writer's choice ("Switch to plain
editor"), a plain textarea holds the same Markdown. The webhook and email
formats are unchanged: Markdown source reaches a chat as literal characters, and
email carries no ticket text.

### Ticket Submission by Role

| Field | Guest | User (logged in) | Staff / Admin |
|-------|-------|-----------------|---------------|
| Name, email, phone | Required / optional | — (uses account) | — |
| Category | Active only | Active only | Active selectable; archived shown greyed |
| Type | — (not shown) | Active only | Active selectable; archived shown greyed |
| Item | — | — (not shown) | Active selectable; archived shown greyed |
| Priority | — (defaults to Medium) | — (defaults to Medium) | Selectable |
| Requester | Their own name and email (above) | Always themselves | Me (default), an existing user, or a typed email address (below) |
| Attachments | — | Yes | Yes |

**Filing on behalf of a requester (#444).** Staff and administrators can open a
ticket for someone else, which is how a phone-in request is recorded. `POST
/tickets` takes one of two optional fields, never both (`400` if both are sent):

- `reporter_user_id`, an existing account. The checks are MCP `create_ticket`'s,
  because they are the ticket service's: an id that is not a live account is
  `400`, and a disabled account is accepted.
- `reporter_email`, an address typed for someone who may have no account. It is
  trimmed and lower-cased, and held to the same address rules as a guest's (a
  display name or a local part that needs quotation marks is `400`). **If the
  address belongs to an account, the ticket is filed for that account**, as if
  its id had been sent, rather than creating a guest identity for a person who
  can sign in. **Otherwise it is filed exactly like a guest ticket**: the same
  creation path, the person is sent the same guest link at that address (read
  and reply, read-only once the ticket closes, replaced when the ticket changes
  in a way they are told about). **`guest_name` is required with every
  `reporter_email`** (`400` without a non-blank one), whether or not the address
  has an account, so that its absence says nothing about the address; it is
  ignored for an account. `guest_phone` is optional and kept. Guest links
  are suspended while guest submission is off (#424), so a typed address with no
  account behind it is `400` then, and nothing is filed; an address that belongs
  to an account is not affected. Only the requester's identity follows the guest
  rules: the ticket is still filed with the caller's staff rights (type, item,
  priority) and routed like any `POST /tickets`.

With neither field the caller is the requester, as before. **The member of staff
is the actor** in the status history, the `created` audit entry and the
`created` event (`ticket.CreateInput.FiledBy`); the requester is the ticket's
reporter or guest, and the staff ticket page shows them (the Requester line,
below). **An existing user who is filed for is told**: someone picked by id, or
a typed address that matches an account, is sent the ordinary "We have received
your ticket" email with a link to the ticket (the sign-in one: they have an
account, so no guest link), queued through the notification outbox like every
other requester mail, so one mail goes to an address even when it is also a
staff recipient of the same change (the staff mail wins). A member of staff who
files their own ticket, with neither field or by naming themselves, is not
mailed about it, as before; nor is a requester who files theirs. A typed
address with no account is filed as a guest ticket and gets the guest link, as
above.

**Budget on the typed-address path (#290).** A typed address mails a stranger
on a staff member's say-so alone, and is also the one way to learn whether an
address has an account, so every attempt is capped: **40 per hour per staff
member**, and the 41st is `429 rate_limited` with `Retry-After` (the time left
in the window) and files nothing. Every `reporter_email` that passes validation
takes a unit before the address is looked up, account or not, and the refusal
for guest submission being off is not given back. A request refused by
validation spends nothing. **A unit is never given back once taken**, not for
the guest-submission refusal and not when `Create` then fails: a refund after
the address is known makes a miss free whenever a failure can be provoked for
one kind of address only. A genuine database error costs one of the 40. A picked `reporter_user_id` is not counted and
is not stopped by a spent budget. The key is the authenticated user, not the
address of the request, so an API key or OAuth client counts against the user it
acts as (the same budget as that person's own session), and another staff
member is unaffected. The figure is fixed, not configurable. Like the credential
limiter it is in memory and per process: a restart clears every count and each
replica counts separately, so N replicas allow N times the cap. The window is
the limiter's fixed one, shared by all keys, so a full budget filed just before
it ends can be followed by another just after. MCP `create_ticket` cannot
file for a typed address (picked users only), so it is not counted.

A reporting
user who sends either field gets `403 forbidden`, the same as any other staff-only
action; it is never ignored, because a ticket quietly filed under their own name is
not what they asked for. A guest (no credential) is refused the same way.

**The Requester line (#444).** The staff ticket page shows a **Requester** line
on every ticket, not only those filed on behalf of someone: the reporting
account's name and email, or the guest's. `GET /tickets/{id}` carries it as
`requester` (`{name, email}`) for staff and administrators only; it is not on
the requester allowed list, so a reporting user is never sent it (they are on
their own ticket, and what they see does not widen), and it is not on a ticket
list. A deleted account is still named; if the account cannot be read the line
reads "unknown" and the failure is logged.

On the New ticket page, staff and administrators see a **Requester** choice
(Me, the default; Another user; Email address) and a requester sees none. Email
address shows a required **Name** and an optional **Phone**, as the guest form
does. Another
user is a search box: `GET /users/lookup?q=` (staff and administrators **in a
signed-in session only**; an API key or OAuth client gets `403 session_required`,
`users:read` or not, `DenyMachineCredentials`) answers with `id`, `display_name` and `email` of at most
ten accounts whose name or address contains the query, ignoring case, ordered by
display name then id. Disabled, deleted and service accounts are excluded by the
query itself (one read, `LIMIT 10`), not read and dropped afterwards. A query
shorter than two characters is `400`, so the endpoint finds a person and cannot
list the directory; deleted accounts, disabled accounts (they cannot sign in) and
service accounts are not found. The search minimum is counted in characters, not
bytes or UTF-16 units. A reporting user gets `403`. The lookup is the picker's:
a disabled account sent by id is still accepted, as MCP accepts one.

**There is no user lookup for machine credentials (#290, Erik 2026-10-14).** A
key that files for someone sends `reporter_email` and the server resolves it:
an account if one exists, otherwise a guest ticket. That leaves no endpoint
that confirms whether an address has an account or lists the directory. The
`201` does carry `reporter_user_id` (the response of a ticket the caller filed
shows who it was filed for), which tells a key that an address has an account.
Erik accepted that on 2026-10-16 ("leak accepted").

What the server does to keep that the only leak: `Server.ResolveOnBehalf` runs
every check that does not depend on the address (subject and description,
priority, the category, type and item existing, belonging together and being
open: `ticket.Service.ValidateCreate`) **before** it looks the address up, so a
request that was going to fail anyway gets the same answer for every address,
over REST and MCP, with guest submission on or off. `guest_name` is required with every
`reporter_email`, checked with the rest, so leaving it out is refused the same
way for every address. Only after all that does the address take a unit of the
40-an-hour budget and get looked up. The answer is then the ticket (`201`, with
`reporter_user_id` when there is an account) or, for an address with no
account, `guest submission is off` (the unit is not given back). So every
probe that learns anything costs a unit, or a filed ticket and a unit, and
probing is bounded at 40 an hour per staff member, over REST and MCP together.
That is what the "leak accepted" ruling covers.

MCP `create_ticket` takes `reporter_user_id` or `reporter_email` (never both),
`guest_name` and `guest_phone`, with the REST rule, from the same code
(`Server.ResolveOnBehalf`, which REST's handler calls too): the same address
rules, the same account-or-guest resolution, the same refusal while guest
submission is off, the same name requirement (always), the same 40-an-hour budget (one
per staff member across REST and MCP), the same created mail. A refusal
comes back as the tool's error text. `reporter_user_id` stays, because existing
callers send it. **When either names someone other than the caller, the caller
is the actor** of the creation, as on REST (`CreateInput.FiledBy`), and the
reporter is who it is for; the account is emailed as above. Naming yourself, or
omitting both, is filing your own ticket and is unchanged.

**Nobody newly picks an archived category, type or item (#421).** Staff and
administrators see archived rows (in the picker and in the catalogue API) so they
can read the classification of tickets already filed under them, but cannot
choose one: creating a ticket under an archived node, or reclassifying a ticket
into one, answers `400` naming the field (`category_id`, `type_id` or `item_id`
`is not an active ...`), for every role and every credential (session, API key).
This replaces the earlier staff exception. The rule is enforced once, in the
ticket service (`refuseArchivedPick`, called by `Create` and `UpdateCTI`), so the
REST API, the MCP `create_ticket` tool and the guest form all obey it. Items are
included; they were checked for nobody before.

Only a **new pick** is refused. A ticket already in an archived node keeps it: a
PATCH that leaves a level unchanged (including one that repeats the existing
classification) is not checked at that level, so an unrelated edit still works,
and a staff follow-up of a closed ticket copies its classification as before. A
reclassify is checked level by level against the ticket's current classification,
so keeping an archived type while choosing a new active item under it is allowed.
A requester's follow-up is unchanged and still needs an active category and type
(#349). OAuth clients cannot create or reclassify tickets yet (they carry no user
identity; see #434), so the rule is not exercised for them until they can.

Attachment upload is available to every authenticated non-guest user on the
API, and the reply composer offers the same control to every role that can
already upload at ticket creation (#175) — staff-only controls in that
composer are the internal-note flag, the customer-notify flag and canned
responses, not the attachment control. A guest cannot attach at ticket
creation (there is no ticket yet to attach to), but can attach to an
existing one afterward at `POST /api/v1/guest/attachments` (only while an
administrator has turned on **Guests may attach files**, off by default, and
within the guest per-file limit; see Guest Submission, #445), authorized the
same way `POST /api/v1/guest/replies` is: the ticket comes from the guest
token in the `Authorization` header, never from an id in the path, so there
is nothing for a guest to change to reach a different ticket. Which types are accepted is an operator setting, `attachment_allowed_types` — a JSON array of lowercase extensions with the leading dot, each matching `^\.[a-z0-9]{1,16}$`. The shipped default is PDF, DOCX, XLSX, TXT, LOG, JPG, JPEG, PNG, BMP; an empty array means this instance takes no attachments at all. `.jpg` and `.jpeg` name one format, so allowing either allows both. Changing it needs a signed-in administrator — an API key cannot widen what the instance accepts. Max 25 MB per file (a guest's maximum is the lower of that and `guest_attachment_max_mb`, 5 MB by default, #445). Images (JPEG, PNG, BMP) are re-encoded to whichever of JPEG (quality 85) or PNG produces a smaller file — except an image with transparency in it, which is always PNG, because JPEG has no alpha channel and "smaller" would be comparing two different pictures. File names on disk are obfuscated (UUID-based); the original file name is preserved in the database for download.

Every attachment upload — authenticated or guest — is authorized the same
way a reply is (`CanUploadAttachment` / `CanGuestUploadAttachment`, reusing
`CanUserUpdate` / `CanGuestUpdate`): a reporting user must own the ticket,
and neither a Closed ticket nor a Resolved one past its reopen window
accepts a new attachment from anybody but staff or an admin. A Closed ticket is
read-only to **every requester** (#349): a guest's upload is refused with the
generic `404` (a link to a closed ticket reads and cannot write), and a
reporting user's with `409 ticket_closed`. Staff and admin are unchanged (#315 — the
upload handler used to check ownership and nothing else, so a reporter
could attach to their own Closed ticket even though the equivalent reply
was already refused).

### Attachment scanning

The scanner is configured with `CLAMAV_ADDR`. Docker Compose ships ClamAV as
an opt-in `antivirus` profile (see #297) — unset by default, so the common
path skips a ~300 MB signature download it may never need. Enabling it is
documented in `docker/.env.example`.

The setting `attachment_scan_address` overrides it and takes precedence once
saved. It has a field under Admin → Settings → Attachments, beside the scan
policy select (#172): a plain text input showing the current value, accepting
`tcp://host:port` or `unix:///path/to/socket`, blank to fall back to
`CLAMAV_ADDR`.

What happens to a file the scanner could not look at is a policy, not an
accident:

| Policy | An unscannable upload |
|---|---|
| `off` | Accepted. Nothing is scanned, and the admin UI says so. |
| `required` | Refused with `503` and `Retry-After`. **Default wherever an address is configured.** |
| `permissive` | Accepted, with a warning logged. |

`required` is the default because configuring a scanner and then accepting
files it could not check is not a position anyone holds deliberately. It is
also what makes the first few minutes of a fresh `docker compose up` safe:
ClamAV spends around three minutes downloading its signature database while the
application is already serving, so uploads wait for the scanner rather than
bypassing it. The help desk works throughout; only the one operation that
depends on the scanner is delayed.

An unrecognised policy value falls back the same way an empty one does, so a
typo cannot silently disable scanning — and the settings endpoint refuses an
invalid value outright, so the operator finds out at save time.

**`GET /api/v1/admin/security-warnings` reports what scanning is actually
doing**, including a live reachability check rather than a restatement of the
configuration: an instance whose scanner container has died has an address
configured and no protection, and those two facts must not look alike.

The admin UI surfaces it (#176): `InsecureConfigBanner` renders a second
alert, alongside the insecure-secrets warning, whenever the operator's own
policy intends scanning (`policy !== "off"`) and the live ping just failed —
the exact combination that used to be invisible, including the `permissive`
case, where uploads keep being accepted without ever being scanned and
nothing before this said so. A deliberate `policy: off` is not shown as a
warning: that is a choice already visible in Settings, not a hidden failure.

**The one exception (#459): a policy nobody chose is warned about.** When no
policy was ever saved (`policy_explicit` is false), no scanner address is set in
either the setting or the environment, and the instance already holds at least
one attachment, the response carries `stale_off: true` and the banner tells the
administrator how to turn scanning back on. This is the state of an install that
upgraded from a release whose Compose file ran a scanner by default: "off" there
is an accident of what was deployed, not a position anyone holds. Explicitly
saving "Do not scan" makes `policy_explicit` true and clears the banner for good,
so a deliberate "off" is still never warned about. Settings → Attachments shows
the same status (policy, configured, reachable, and the `effect` sentence) so an
administrator can check it without waiting for an upload to fail.

**Attachments are download-only. There is no previewer, and there will not be
one.** (Markdown text follows the same rule for images: an image in a post is a
link, never loaded. See *Ticket text formatting*.)

No inline rendering of any attachment, images included: no lightbox, no
thumbnail, no `<img>` pointing at the download route, no PDF viewer. Every link
to an attachment downloads it. The download response carries
`Content-Disposition: attachment`, and that header is a contract rather than a
convenience — a test fails if it is removed.

This is a deliberate trade of a small convenience for a whole class of bug.
Rendering attachment content on the help desk origin means any file that
reaches a browser is a candidate for stored XSS against the staff sessions that
live there, and the defence becomes a content sanitiser that has to be right
forever. A previewer would also need either a separate origin or a sandboxing
CSP — the shape the logo route already uses, being the one place this
application does render an uploaded file inline. Real complexity, for a feature
nobody has asked for.

The corollary is that the **ticket attachment** allowlist is not a security
boundary and does not have to be a sanitiser. Nothing is rendered, so what the
list decides is which files this deployment is willing to hold — a policy
choice, which is why it belongs to the operator rather than to a Go file. An IT
team triaging a suspicious `.exe` has a real reason to accept one; a deployment
that wants PDF and nothing else has an equally real reason to say so.

That is what made the list safe to hand over. While `Content-Type` came from
the claimed extension, the list *was* the boundary and widening it was a
security decision rather than a policy one — an operator allowing `.html` would
have been allowing this origin to serve `text/html`. Once every download is an
opaque blob with an attachment disposition, there is no set of types that is
dangerous to the server, so there is nothing left for a code-side list to be
the outer bound of.

The case that makes this load-bearing is **PDF**. `application/pdf` opens in the
browser's built-in viewer, and those viewers run JavaScript, so a malicious PDF
rendered inline would execute on this origin.

Three things stop it, all of them tested:

- `Content-Type: application/octet-stream` on every attachment, whatever the
  file claims to be. No browser renders that. Until #165 step 1 the type came
  from the filename, so a PDF went out as `application/pdf`.
- `Content-Disposition: attachment`, which tells the browser to save rather
  than open, and carries the original filename (RFC 6266, both forms).
- The download route **refuses** a request whose `Sec-Fetch-Dest` says the
  browser intends to render the response, and sends `Vary: Sec-Fetch-Dest` so
  the browser cache cannot answer a rendering request out of an allowed one.
  The two headers above are obeyed for a navigation and ignored for a
  subresource, so `<img src>` or a CSS `url()` would otherwise render an
  attachment regardless of what the server said. An allow list of `document`,
  `empty` and absent, so a destination nobody has invented yet is refused.

Two limits, stated because they are easy to forget:

- **Absent is allowed**, so the check does not apply to command-line clients or
  to browsers older than Chrome 80, Firefox 90, Safari 16.4. Refusing an absent
  header would break every one of those, which is worse; the check protects
  what it can reach.
- **Script can fetch the bytes itself** — `Sec-Fetch-Dest: empty`, which has to
  be allowed or downloading stops working, because an `<a download>` click
  sends `empty` too — and render them without asking again. The server cannot
  tell that apart from a download. The same goes for a service worker, which
  never sees these headers at all and can replay a cached response to an
  `<img>`; it is same-origin page script, so it adds no capability script did
  not already have. A frontend test fails on the obvious shapes of that
  mistake, but it reads source text and cannot follow a value between files,
  so it catches carelessness at review time rather than being a control.

Three caveats:

- **Content detection, described in full below, reads a prefix rather than the
  whole file.** It is confident about formats that declare themselves in their
  first bytes, and it is not a parser: a file that is a valid one thing and a
  usable other thing is beyond it. Content it cannot place at all is recorded
  as unidentified, which counts as a contradiction rather than as a pass.
- **Attachments uploaded before any of this carry none of it** — no detected
  type, no hash, no mismatch answer. That renders as an absence and never as a
  finding: nobody looked, and "not recorded" and "nothing wrong" are different
  statements. Nothing is backfilled and nothing is re-scanned.
- **The logo is the exception**, and the only upload this application renders
  inline. #165 removed SVG from that uploader: it was accepted after being
  parsed and pattern-matched for scripts, event handlers and `javascript:`
  URIs, and pattern-matching for dangerous SVG is a game you have to keep
  winning — the 1.2.0 advisory already contains one escape from it. PNG, JPEG
  and GIF are accepted, all re-encoded as PNG. The route keeps its own
  sandboxing policy (`sandbox; script-src 'none'`) on top of that, because what
  decides those bytes are an image is a four-byte magic check rather than a
  proof.

### What an upload actually is

Every upload is inspected as it arrives, and what is found is recorded beside
what the file claimed.

**The detected type** comes from a content detector that reads a prefix of the
file and returns its own canonical extension for the format it recognised —
`.docx`, `.html`, `.exe` — together with the bare media type (`text/html`,
never `text/html; charset=utf-8`; the parameters are noise in a column the UI
renders). Returning an extension is the point of it: the comparison against the
claimed extension is then extension against extension, with no table mapping
one detector's MIME vocabulary onto another's for somebody to keep correct.
It replaced a hand-written check that matched a signature per extension and
ended in `default: return true`. That was not a text special case but a
default, so every extension without an entry was never looked at — `.txt` and
`.log` then, and whatever an operator adds now, which is what made the default
a hole rather than a gap.

**The SHA-256** is the hex digest of the same bytes. It is what an analyst
looks a sample up by, what a chain-of-custody record has to state, and the key
the reputation cache below is kept under.

Both are computed on the bytes as uploaded — before an image is recompressed,
before a wrap puts the file inside an archive — because both answer the same
question: what did this person actually send us. The consequence is stated
rather than left to be discovered. For a recompressed image the stored file's
hash is **not** the hash of the file on disk; for everything else the two are
the same.

Content nothing recognises is an answer and not a failure. It is recorded as
`application/octet-stream` with no extension. Under a name that claims a binary
format it counts as a contradiction, because saying nothing there would make an
unidentifiable file look like a verified one. Under a text extension it does
not — see the third relaxation below.

**A content mismatch** is the detected extension differing from the claimed
one, after three relaxations and no others:

- `.jpeg` and `.jpg` collapse into one. The only synonym, and it is there
  because the two spellings name a single format — the same normalisation the
  allowlist applies, so allowing either allows both.
- A text extension — `.txt`, `.log`, `.csv`, `.md` — matches any content that
  is inert text, and *inert text is whatever the detector says it is*. The
  library arranges every type it knows into a tree; a type whose ancestry
  passes through `text/plain` is one it is willing to call text. That covers
  `text/*`, and also `application/json`, `application/x-ndjson`,
  `application/geo+json`, `image/svg+xml`, `application/xhtml+xml` and
  `application/x-subrip` — text the IANA registry happens to file elsewhere.
  It does not cover a ZIP or anything packaged inside one, which is the case
  that decided it: a Visio drawing is registered as
  `application/vnd.ms-visio.drawing.main+xml`, so any rule reading the name
  rather than the ancestry lets an archive through under a `.txt` name while
  flagging a plain ZIP under the identical one. **`text/html` is included**,
  and used not to be. The exclusion was argued from "HTML runs when
  it is opened", which is false in the way that decides this: what opens a file
  is chosen by its name, not by its content, so `notes.log` opens in a text
  editor whatever bytes are inside it. Nothing here renders an attachment
  either. The exclusion protected nothing and flagged a captured HTTP response
  saved as a `.log` — this document's own example of an ordinary attachment.
- Content nothing recognised at all, under a text extension, is not a
  contradiction. The detector failing to place a file is a limitation of the
  detector rather than evidence of deception, and it takes very little: one NUL
  byte from a process that died mid-write, UTF-16 with no BOM. Under a claimed
  binary format an unplaceable file stays a mismatch — a PDF that cannot be
  identified as a PDF is worth a sentence.

A text extension is not a blanket pass: a rotated log compressed in place and
still named `.log` is detected as `application/gzip`, which is not inert text,
so it is flagged — and, being text-named, it is stored under its own name
rather than wrapped or refused.

The text relaxation (the second) is decided by asking the detector, not by a
list of our own, and the difference is not cosmetic. A list of acceptable
detected *extensions* called a container log (`application/x-ndjson`), a config
file (`text/xml`) and an exported contact (`text/vcard`) files lying about
themselves; a list of media types (`text/*` plus JSON and NDJSON) flagged a
GeoJSON document; accepting any name ending `+json` or `+xml` admitted the
Visio drawing above, which is an archive. The tree has no such gap, because it
is the same source that produced the media type being judged: a rule written
from the detector's own answers cannot disagree with the detector. A warning
that fires on ordinary files is one staff learn to click past, which is worse
than no warning, so where a legitimate case fires, the fix is to widen one of
these two rules and never to soften the flag.

The answer is recorded at upload rather than recomputed on read, because a file
this application renamed has lost the name the uploader claimed: recomputing
would compare `.zip` against the content and report a truthfully named sample
as lying about itself.

Recording a mismatch never refuses an upload, and the recording is not
optional: `detected_mime`, `sha256` and `content_mismatch` are written on every
file this instance stores, whatever the operator's settings say. They are facts
about what arrived rather than enforcement.

Whether a mismatch is then refused or stored is a separate decision, and it is
the operator's: see the second tier below.

### The three tiers an upload is stored in

Wrapping and recompression are alternatives rather than steps: a wrapped file
is an archive, so recompressing one would mean handing a ZIP to the JPEG
encoder. The upload handler takes the first of these that applies.

1. **The scanner identified it**, and the operator set
   `attachment_infected_handling` to `quarantine`. The sample is wrapped in a
   ZIP with the password `infected` and stored under the uploaded name plus
   `.zip` — `sample.exe` becomes `sample.exe.zip`, so nothing downstream
   double-clicks an executable. The scanner's name for the detection is stored
   with it. Under the default, `refuse`, an infected upload is rejected with
   `422` and never reaches this tier. The setting only ever decides what to do
   with a verdict the scanner actually returned: with the scan policy `off`, or
   the scanner unreachable under `permissive`, nothing is ever identified as
   infected and `quarantine` does nothing at all.
2. **The name claims a binary format, the content contradicts it, and the
   detected type is not on the operator's allowlist**, and the operator set
   `attachment_mismatch_handling` to `wrap`. The file is wrapped with no
   password and stored as `suspicious-<crc32>.zip` — the CRC32 of the file
   inside, which every ZIP entry already carries, so the archive is named after
   a value its recipient can verify and it costs nothing to produce. Under the
   default, `refuse`, the upload is rejected with `415` `invalid_file` and
   never reaches this tier.

   **A claimed text extension never reaches this tier at all**, under either
   value of the setting. Wrapping contains a file by taking away the name that
   decides how it opens; `crash.log` already opens in a text editor whatever is
   inside it, so there is nothing to contain, only something to say. Such a
   file is flagged if its content contradicts the name — when the name is one
   this project ships, which `.txt` and `.log` are and `.csv` and `.md` are
   not; see the paragraph on shipped extensions below — recorded either way,
   and stored under its own name.
3. **It is an image** — `.jpg`, `.jpeg`, `.png` or `.bmp` — and neither of the
   above. It is recompressed to whichever of JPEG (quality 85) or PNG is
   smaller, under the name it was uploaded with.

Anything else is stored exactly as it arrived.

**Which mismatches are wrapped and which are only flagged is the distinction
people will get wrong.** Not every mismatch is wrapped. The judgement is the
operator's own allowlist, which means there is no second list to keep correct:
a file is wrapped when the type it turned out to be is not a type this instance
accepts, in the detector's spelling of that type — and then only under a name
claiming a binary format. So HTML inside a
`.pdf` is wrapped on an instance set to `wrap`, because `.html` is not an
accepted type; on a default instance it is refused with `415` instead, which is
the same condition and the other action. A real PNG inside a `.pdf` is a
mislabelled file of an accepted type: flagged on the row, stored under its own
name, not wrapped, under either value. And a `.log` holding a captured HTML
response is an ordinary help desk attachment — neither flagged nor wrapped nor
refused, because `.log` is a text name and `text/html` is inert text.

**Wrapped or refused is an operator setting, and refusing is the default.**
`attachment_mismatch_handling` takes `refuse` (the default) or `wrap`:

| Value | Behaviour |
|---|---|
| `refuse` | **Default.** `415` `invalid_file` — the same status, code and message 1.2.0 refused with, so whatever an operator has wired into that response still reads it. |
| `wrap` | Accepted, stored as `suspicious-<crc32>.zip`, flagged. |

Deliberately the same shape, the same words and the same default as
`attachment_infected_handling`, because it is the same decision about a
different question: does this instance store a file it has reason to distrust,
or turn it away? An operator who has reasoned about one does not have to start
over on the other. Both are session-gated — an API key cannot change what this
instance will hold — an unrecognised value falls back to `refuse` rather than
to the permissive option, and the settings endpoint refuses the write outright
with `invalid_mismatch_handling`, following `invalid_scan_policy`.

`refuse` is the default because relaxing a security control in an upgrade
nobody opted into is the wrong default for a behaviour only some deployments
want.

**What a default instance does with a contradicting upload.** The judgement is
the content against the operator's allowlist, so it moves rows in both
directions compared with 1.2.0's hard-coded signature check. What the upload
handler does on a default instance (these rows were measured through the
handler when content detection was introduced, #171):

| Upload | Result |
|---|---|
| plain text named `.pdf`, `.docx` or `.xlsx` | `201`, stored under its own name, flagged |
| a real PNG named `.jpg`, or a real PNG named `.pdf` | `201` |
| a plain ZIP named `.docx` | `415` |
| under four bytes, text-looking, under a text or document name | `201` |
| under four bytes, unplaceable, under a binary name | `415` |
| under four bytes, text-looking, under an image name | `422` |
| under four bytes, unplaceable, under an image name | `415` |
| text named `.png` or `.jpg` | `422` `invalid_image` |
| a `.txt` or `.log` of four bytes or more, whatever is inside it | `201` |
| HTML named `.pdf` | `415` |

The first row is the one to understand rather than to fix. A `.pdf` holding
plain text is a contradiction and is flagged, and it is neither wrapped nor
refused, because the judgement for tier 2 is the operator's own allowlist and
`.txt` is on it: the content is something this instance would have accepted
under its own name, so there is nothing to contain, only something to say.
Refusing it would need a second list of "types that may not hide inside other
types", which is the second list this design exists to avoid keeping correct.
The third row is the stricter direction and is deliberate: any ZIP used to pass
as a `.docx` because both start `PK\x03\x04`, and an Office document is now
identified as an Office document.

**Containment only applies to the nine extensions this project ships.** Each
was checked against the detector, so a contradiction under one of those names
is one we can stand behind. An extension an operator adds is neither contained
nor flagged — the detector reports one canonical spelling per
format, so `.htm` is HTML and `.tif` is TIFF but it calls them `.html` and
`.tiff`, and an operator who allowed `.htm` and received genuine HTML would
otherwise be refused with a message saying the content did not match the name.
It matched exactly. A bigger synonym table is not the fix: the two libraries
involved do not agree on a name for the same format — Go's standard library
calls a Windows executable `application/x-msdownload` where the detector calls
it `application/vnd.microsoft.portable-executable` — so there is no canonical
mapping to build one from.

The invariant is narrower than "their spellings are the detector's own", and
the difference is worth stating because it is what a future reader would check:
the detector calls a `.log` a `.txt` and a `.jpeg` a `.jpg`, so two of the nine
are not its spelling at all. They survive because the relaxations above cover
them. The real rule is that every shipped extension is either the detector's
spelling **or** covered by a relaxation, and a test walks the shipped list and
fails on any entry that is neither — because adding `.tif` to the defaults is
an entirely reasonable thing to do, and would otherwise contain and refuse
every genuine TIFF.

**What this gives up.** An operator-added extension is no longer contained even
when the content genuinely contradicts it. The case worth naming: an instance
that has allowed `.xml`, receiving an XHTML document carrying a script under
the name `report.xml`. It is stored under that name, and a browser opening it
from disk will run the script. That is bounded — allowing `.xml` already allows
XSLT-bearing XML, which does the same — and it is the price of not refusing
genuine files with a false explanation. An operator who wants containment for a
type gets it by that type being one we ship.

Neither the containment nor the flag applies to an extension we did not ship.
"No contradiction" is a claim too, and it is not one we can make about a
spelling we cannot check: the row carries the detected type and no verdict,
which reads as "we looked and could not judge" and is distinguishable from a
row that predates detection and has neither.

**Why `wrap` exists at all.** Refusing closes off the case this product is
otherwise good at — the suspicious file a user reported is exactly the file a
ticket is about — and merely flagging it would be too quiet, because the file
still lands on somebody's disk called `report.pdf`. The archive name is the
warning, it cannot be double-clicked into whatever the file actually is, and
unlike our UI it survives being forwarded or saved to a share. That is a real
scenario for an IT or security team whose tickets are *about* suspicious files,
and a minority one, which is exactly what a setting is for.

**What the setting does not govern.** It swaps the action on one condition and
changes nothing else. A mismatch whose detected type *is* on the allowlist — a
real PNG named `.jpg` — is flagged and stored under its own name under both
values: there is nothing to contain there, only something to say — with the
spelling caveat below. And a file
the scanner identified is governed by `attachment_infected_handling`; this
setting never applies to it. The two are independent because the claims are
different — "the scanner named this" and "the content is not what the name
says" are not the same fact — and a file that is both is quarantined.

**"Accepted" there means the detector's spelling.** The question the escape
asks is whether the format the file turned out to be is one this instance
accepts, and it is answered by looking the detector's extension up in the
operator's list. Those are two vocabularies. An instance that allows `.pdf`
and `.htm`, receiving genuine HTML named `report.pdf`, refuses it under
`refuse` and wraps it under `wrap`; an instance that allows `.pdf` and `.html`
flags the same file and stores it under its own name. Both operators allowed
HTML. Only one spelled it the way the detector does.

This is deliberate and it is not a synonym table. The same table was tried and
rejected for containment itself, for the reason given above — the two libraries
involved do not agree on names for one format, so there is nothing canonical to
build it out of — and a table here would have the same problem with a worse
consequence, since guessing that two spellings mean one format is how a real
contradiction stops being contained. The error runs in the strict direction: a
file only reaches this question by already lying about its name, and the worst
outcome is that a lying file is contained on one instance and merely flagged on
another. The spellings where it bites are the ones where the detector differs —
`.htm`, `.tif`, `.mpg` — and an operator who wants the lenient answer
gets it by adding the spelling the detector uses.

**Why the password is published.** `infected` is in this document, in the issue
and in the UI beside every quarantined file. It protects nothing and is not
meant to. Its only two jobs are that the stored bytes are not directly
double-clickable, and that an on-access scanner — on our storage or the
downloader's — does not eat the sample out from under the ticket a day later.
It is the long-standing convention for moving samples, so an analyst's tooling
already knows what to do with it. The encryption is ZipCrypto rather than AES
for the same reason: weak is fine when the password is public, and an AES-256
ZIP is opaque to Windows Explorer and macOS Archive Utility, which is precisely
the friction this is meant to remove. A wrong password does not reliably fail
to open a ZipCrypto archive either, which is irrelevant here and must not be
read as confidentiality.

**Why the second tier has no password.** A mismatched file is not known-bad; we
could not identify it, which is a different claim. It is undouble-clickable
either way, and blinding the recipient's own antivirus over a wrong extension
would be the wrong trade.

The sample keeps its own name inside the archive, so unwrapping produces the
file the ticket is about rather than our wrapper's name. The appended `.zip`
describes the wrapper and not the sample: it is added after the allowlist has
had its say on the uploaded name and after the hash and detected type were
taken, so `.zip` does not have to be an accepted type for either tier to work.
Wrapping is not optional per file — when the setting says `quarantine`, or
`wrap`, every upload that meets the condition is wrapped, because a setting
that can be bypassed for one file is a setting nobody can reason about. Neither
tier is retroactive in either direction: turning quarantine or the wrap on does
not rewrap what is already stored, and turning either off does not unwrap it.

What the row says afterwards. `mime_type` describes the file as stored;
`detected_mime` describes the bytes that arrived; `size_bytes` is the stored
file, because that is what a download costs:

| | stored name | `mime_type` | `detected_mime` |
|---|---|---|---|
| ordinary PDF | `report.pdf` | `application/pdf` | `application/pdf` |
| PNG recompressed to JPEG | `shot.png` | `image/jpeg` | `image/png` |
| HTML named `.pdf`, under `wrap` | `suspicious-4f2a91c3.zip` | `application/zip` | `text/html` |
| infected `.exe`, quarantined | `sample.exe.zip` | `application/zip` | `application/vnd.microsoft.portable-executable` |

A recompressed PNG is not a mismatch: detection ran on the uploaded bytes,
which were a PNG, under a claimed `.png`. An infected file that was named
truthfully is not a mismatch either — the two tiers answer different questions,
and a file can be both, in which case it is quarantined and the mismatch is an
extra line on the row rather than a replacement for the malware warning.

Where the built-in extension-to-MIME map has no entry — which is every type an
operator adds — `mime_type` falls back to what the detector said, and to
`application/octet-stream` when it could not place the bytes either. A blank
type column is not an answer. None of this is a decision about how the file is
served: every download is `application/octet-stream` regardless.

### Reputation lookup

A quarantined sample is worth asking the world about, so the SHA-256 can be
looked up at third-party reputation services. **Four independent toggles**, a
key for each of the three commercial providers, and one shared refresh
interval — all session-gated like the scanner keys, because an API key must not
be able to decide where customers' file hashes go, or how often:

- `attachment_reputation_virustotal_enabled` + `attachment_reputation_virustotal_key`
- `attachment_reputation_metadefender_enabled` + `attachment_reputation_metadefender_key`
- `attachment_reputation_polyswarm_enabled` + `attachment_reputation_polyswarm_key`
- `attachment_reputation_circl_enabled` — **no key setting at all.** hashlookup
  authenticates nobody, so there is none an operator could supply, and an empty
  box somebody feels obliged to fill is worse than no box.
- `attachment_reputation_refresh` — how often a stored verdict is asked about
  again. An unrecognised value is refused at save time with
  `invalid_reputation_refresh` and read as the default, never as `never`: a
  typo must not silently switch re-checking off, because the symptom is a
  verdict that looks current for as long as the instance lives.

The keys are write-only over the API, like the OIDC client secret and the SAML
key, and are never logged.

**Enabling a provider requires its key.** Turning VirusTotal on with no key is
refused at the write with `invalid_reputation_config`, naming which provider is
missing one — the same rule as everywhere else in that handler, a setting
accepted and then ignored being worse than a refusal. It disposes of a state
that existed before, "enabled but silently doing nothing", by making it
unreachable. Separate keys are the other half of what one selected provider
could not do: switching services no longer destroys the key you had already
pasted, and an operator can hold keys for two without choosing between them.

The rule is checked for each provider whose toggle or key the write includes. A write that mentions neither is not refused because of that provider's stored state ([#394](https://github.com/PubliciaLLC/go-help-desk/issues/394)). Only a database edit can leave a provider enabled with no key, and checking it on every write refused them all, including the one that would fix it. Such a provider does no lookups.

**Every enabled provider is queried, and every answer is stored.** They answer
different questions, which is the point of allowing more than one: VirusTotal
counts engines, CIRCL says whether a catalogue has the file on record, and a
sample one calls `detected` while another calls `known` is telling staff
something either alone would hide. The cache is already keyed
`(sha256, provider)`, so this needs no schema change — one row per provider per
file, each with its own expiry clock and its own re-check. The cost is real:
four enabled providers means four lookups per quarantined file on first view,
against caps of 500/day (VirusTotal's own published figure), 4,000/day and
300/hour (ours — OPSWAT and CIRCL publish no number), and 60/hour
(PolySwarm's own).

**The row shows the worst verdict; one click shows them all.** Inline, staff see
the single most serious answer across every provider that replied:

```
detected  >  unseen  >  unscanned  >  clean  >  known
```

`unseen` outranks `clean` because only quarantined files are looked up, so every
hash here is one ClamAV already called malicious — a file no service has ever
seen is a novel sample, more concerning than one seventy engines examined and
passed. `known` is the floor because it is the only positive claim in the set;
everything else is an absence of findings. And **`unavailable` is not in the
ordering at all**: it is a failed lookup rather than a verdict, and it must
never displace a real answer, so VirusTotal timing out while CIRCL says `known`
reads `known`. It is the inline answer only when nothing answered, and it is
always listed against its own provider, because an operator needs to see their
key failing.

The expanded view carries each provider on its own line — named, with its own
verdict, its own `analysed_at`, its own `fetched_at`, its own link and its own
*Check again* control where the state allows one.

The wire carries an `inline` flag saying which of those lines the one-line
summary was taken from, and **nothing renders it yet**. The intent is that the
summary's source is marked, so the row's single sentence does not look as
though it came from nowhere; today a reader has to work it out from the
verdict ordering. Tracked as an issue. Stated here rather than left implied
because this document is what the next person builds from, and a sentence
describing a marker that does not exist is how a gap becomes invisible.

**All four off is a supported configuration, not a broken one.** It means
attachments are judged by this instance's own scanner alone, which is a complete
answer and the legitimate choice of an operator who cannot send customer file
hashes anywhere. There is no warning, no banner, and specifically no "not
checked yet" — that phrase belongs to a lookup that was attempted and did not
finish, and nothing was attempted. The reputation block is absent from the
payload entirely. The admin settings page says so plainly where the toggles are:
*with none enabled, attachments are judged by this instance's own scanner
alone.*

**The VirusTotal hash link does not follow the toggles**, and it is not on
every attachment either — those are two separate rules and both matter. On the
rows that carry it, it carries
`rel="noreferrer noopener"`. A link is not a lookup. A lookup is this server
sending a customer's file hash to a third party — the operator's decision, their
allowance, and what the toggles govern. A link sends nothing from this server:
it is an anchor the analyst clicks in their own browser, under their own account
or none, exactly as if they had copied the hash off the page and pasted it
themselves, which they can do anyway because the hash is there with a copy
control. Disabling VirusTotal as a lookup provider means *do not send my
customers' hashes to VirusTotal from my server*; it does not mean *my staff may
never look at VirusTotal*. VirusTotal specifically because its page is the one
every analyst already knows — no account needed, the complete report renders
logged out — where MetaDefender's public page announces itself as a reduced view
and CIRCL has no per-hash web UI at all. One line of copy beside it says the
link opens in the reader's own browser and that this instance sends nothing
there unless VirusTotal is enabled above; without it, an operator who switched
VirusTotal off and still sees the link will reasonably conclude the setting does
not work.

**It is not on every attachment, though.** A link goes only where this instance
itself found something worth a second opinion: the scanner named the file, or
its content contradicts the name it arrived under. An ordinary attachment —
content matching its name, scanner passed — carries the hash and no link,
because a link and a line of explanatory text under every holiday-request PDF
is the noise that teaches people to stop reading the rows that matter.

Note what is deliberately not consulted: the reputation verdict. Only
quarantined files are ever looked up, so an ordinary attachment could not have
one — and on a quarantined file, hiding the link because some provider said
"clean" would be second-guessing the analyst doing the triage.

An instance that set the earlier `attachment_vt_lookup` / `attachment_vt_api_key`
keys, or the `attachment_reputation_provider` / `attachment_reputation_api_key`
pair that replaced them, is not migrated. Nothing reads any of the four, they are
removed a release later with a row-deleting migration, and an operator has to
re-enter their key against the provider they want: copying somebody's secret from
one setting to another on their behalf is not something to do quietly.

CIRCL is a **catalogue and not a scanner**, which is why it answers with fewer
states than the others: `known` when a hash set it re-publishes carries the
hash, `unseen` when none does, and `unavailable` when it could not be asked. It
never says `clean`, `detected` or `unscanned`, because no engine runs there and
there is nothing for one to have found or missed. Two things follow that the UI
has to respect. Its `unseen` is **weaker** than the others' — NSRL's legacy
sets are SHA-1 indexed and the SHA-256 mapping was added afterwards, so a file
can be in NSRL and still answer 404 — which is why it reads as "CIRCL has no
record of this SHA-256" and not "this file is unknown". And asking it is a
**disclosure of a different kind**: it records the caller's IP and User-Agent,
and its public instance serves a leaderboard of the most-queried hashes with
filenames, where VirusTotal and OPSWAT log but do not publish. Hash reputation
data from CIRCL hashlookup, Computer Incident Response Center Luxembourg,
CC-BY-4.0.

What the lookup does:

- **Lazy, and only where it is worth spending.** A lookup happens when staff
  open a ticket carrying a quarantined attachment with no current verdict —
  never at upload, never on the ordinary attachments, and never for a reporting
  customer looking at their own ticket, because that would spend an operator's
  allowance on a page refresh. There is no reputation queue: the project's
  background workers (the notification outbox worker and the periodic sweeps)
  do none of this work, so a lookup is made inside the request that needs it
  (#126).
- **Cached against the hash and the provider**, not against the attachment, so
  the same file on five tickets costs one lookup per provider. One row per
  provider per hash is what lets four services be asked at once without either
  one's answer being attributed to another, and it is what gives each verdict
  its own expiry clock and its own *Check again*.
- **Budgeted.** Each provider is metered at its own published free-tier
  ceiling, in that provider's own shape: 500 a day for VirusTotal plus a
  four-a-minute bucket, 4,000 a day for MetaDefender with no bucket — a
  courtesy cap of ours, since OPSWAT publish only "a limited number of API
  calls per day" — and 60 an
  hour for PolySwarm, which publishes no daily figure at all and so is given
  none. CIRCL publishes no ceiling of any kind, so its 300 an hour is a
  politeness cap of ours rather than a limit of theirs — a free best-effort
  service run by a CERT should not be metered by nothing. The daily counters
  reset at 00:00 UTC and the hourly ones on the hour.
  In memory and per process: it is a courtesy cap rather than an accounting
  record, and a restart spending a handful of extra lookups is cheaper than a
  table. **Per provider, because the allowances are:** an exhausted VirusTotal
  bucket does not stop CIRCL answering, and the row then shows CIRCL's verdict
  rather than nothing.
- **Expiring.** `attachment_reputation_refresh` decides when a stored verdict
  is asked about again: `weekly`, `biweekly` (the default), `monthly`,
  `quarterly` or `never`. A verdict decays, which is the whole reason this
  exists — new signatures catch old malware, and the sample nobody had
  submitted when we asked is precisely the one submitted a week later.
- **Except a detection or a `known` file, which never expire.** Engines do not
  un-flag a file, so re-confirming known malware is the one lookup guaranteed
  to tell nobody anything; and a hash does not fall out of a vendor catalogue,
  so re-confirming a catalogue entry is the other. Both would be spent out of
  the same allowance as the lookups that would tell somebody something.
- **Re-checkable by hand.** Staff get a *Check again* control on a verdict that
  can still change, and the server allows one re-check per hash every seven days
  whatever the interval says — including when it says `never`, because an
  operator who turned automatic checking off to save quota did not mean that
  nobody may ever ask. It is a floor and not an override: the daily budget
  still applies, a re-check inside the week is refused with the date it clears,
  and a re-check of a verdict that cannot change — a detection or a `known`
  file — is refused outright. The control is disabled
  rather than hidden while the week runs, because a control that vanishes
  teaches nobody anything, and it is absent where there is no verdict to
  refresh.

A verdict is one of five things a provider can tell us apart from failure:
never seen this hash, seen it but holding no verdict, seen it and no engine
flagged it, seen it and some did, or **known** — a named vendor feed has this
exact hash in its catalogue. The distinctions are the value of the feature and
none of them may collapse into the others.

**`known` is the one verdict here with somebody standing behind it**, and it
is an exception for a reason worth stating: a named feed made a positive claim
about the file. Everywhere else in this feature an absence must never read as
safety — `clean` only means engines ran and found nothing, `unseen` only means
nobody has submitted it — and none of those has anybody behind it.

It does not render as reassurance, and that is deliberate. Only a file the
local scanner has already flagged is ever looked up, so a catalogue hit is
never the stronger claim: it is context on a sample somebody has already
called malicious, which is a reason to look harder rather than a reason to
relax. The row says so — amber, with the caveat attached — and a test pins it.

It is `known` and deliberately **not** `known_good`, because how much the claim
is worth depends entirely on which feed is speaking, and the state name must
not overclaim on the weakest of them. PolySwarm's `microsoft_windows` feed is
an Authenticode signature assertion — a positive claim that the file is signed
and trusted. An NSRL catalogue entry means only that the file appeared in a
known software distribution, and NSRL catalogues hacking tools; that is not a
statement that the file is safe.

So one state, and the feed names travel with the verdict and carry the weight.
A renderer must say which feed is speaking — "known file, signed by Microsoft
Windows" against "known file, catalogued by NSRL" — because those two sentences
are not worth the same and neither of them is "known good". A bare `known` with
no feed named would be a claim from nowhere, which is the shape this feature
refuses everywhere else.

Two providers produce it today: PolySwarm, from its `KNOWN_GOOD` state, and
CIRCL, when a hash set it re-publishes carries the file. VirusTotal and
MetaDefender never return it.

**What it does not do**, stated plainly because every one of these is the
mistake that has already been made twice in the virus scanner:

- No key means no lookup at all. The page renders, the hash and the link are
  still there, and no request is made.
- A lookup that failed, timed out, was rate-limited or ran out of daily budget
  renders as **not checked**. Never as clean. "Clean" is reachable only from a
  completed lookup that came back with no detections, and an unavailable answer
  is never written to the cache — caching a transient failure against a verdict
  that is kept would freeze it permanently.
- A verdict that has passed its interval but could not be re-checked stays on
  screen rather than disappearing. A re-check that cannot be made — no budget,
  provider down — must not delete a real answer from the page, and "clean,
  and here is when the service last analysed it" is worth more to the reader
  than "not checked yet".
- Nothing is uploaded. A hash lookup tells a provider that somebody has seen a
  file they already hold; submitting the file hands them a customer's document,
  which is a different feature with different terms attached and is
  deliberately not built.
- Nothing is re-scanned locally. The ClamAV verdict is recorded when the file
  arrives and never revisited, so a file that was clean last month and would be
  recognised today still reads as it did on the day it was uploaded. The
  reputation lookup is the only part of this that expires.
- A slow provider cannot hold a page open. The whole of one request's
  reputation work shares a five-second budget, not five seconds each: lookups
  run one after another, so without a shared bound three quarantined
  attachments and two unreachable providers is six fifteen-second timeouts and
  a response the server can no longer write. Measured at ninety seconds before
  the bound and five after. What the budget covers is the outbound call and
  nothing else — a verdict already in the cache is still served after the time
  is gone, because a hung provider must not erase answers we already hold.

The verdict carries the provider's name so the UI can attribute it —
"VirusTotal has never seen this file" is a claim with a source, and the generic
version is a claim from nowhere. The name and the finished link URL are built
by the server, because the provider is an admin-only setting staff cannot read
and a frontend that rebuilt the URL itself would hold a second copy of provider
knowledge to drift from the first.

---

## API

### REST API

Serves both the frontend SPA and external integrations.

**Public endpoints (no session or credential):**
- `GET /health` — liveness, `{"status":"ok"}`. Outside `/api/v1`.
- `GET /api/v1/site` — branding info, app version, `guest_submission_enabled` and the guest attachment setting (`guest_attachments_enabled`, `guest_attachment_max_bytes`, `guest_attachment_max_per_ticket`, `guest_attachment_uploads_per_hour`); used by the SPA shell and login page before authentication
- `GET /api/v1/logo` — the stored logo (PNG), with a 5-minute cache header
- `GET /api/v1/setup/status` — whether first-run setup is needed; `POST /api/v1/setup` creates the first administrator (and the first category, see Ticket Classification). It answers `409 already_configured` once **any** user row exists, disabled and deleted accounts included, so setup never reopens.
- `/api/v1/auth/*` — `POST /local/login`, `POST /local/logout`, `GET /providers`, `POST /oauth/token` (OAuth client credentials), the SAML and OIDC sign-in routes, and `GET /signup/status`, `POST /signup`, `GET` and `POST /verify-email` (see Authentication). `POST /local/mfa/verify` and `POST /local/passkey/*` need the session the password step produced and are refused to machine credentials.
- `/api/v1/guest/*` — `POST /tickets` (submit) and `POST /resend` (re-request a link) are public, throttled, and answer `404` while guest submission is off. `GET /ticket`, `POST /follow-up`, `POST /replies` and `POST /attachments` are authenticated by the per-ticket link token alone (see Guest Submission); `POST /follow-up`, `GET /ticket`, `POST /replies` and `POST /attachments` answer `404` while guest submission is off (#424).
- `GET /api/v1/categories` and its `/{id}/types` and `/{id}/types/{typeId}/items` children — public **only while guest submission is on**, because the guest form needs them; otherwise they require an authenticated caller (a session, API key or OAuth token). Active entries for a guest or a reporting user, all of them for staff and administrators.

Everything else under `/api/v1` requires a session, API key or OAuth token, and the role and scope rules below.

**Unknown settings keys.** `PATCH /admin/settings` accepts only the keys declared as `Key*` constants in `internal/domain/admin/settings.go` (`admin.KnownKeys()`, held equal to the constants by a test, with further tests that every key the Settings page sends and every key a migration seeds is in it). A body with any other key is refused whole with `400 unknown_setting` naming the first such key (sorted), and nothing is written, the valid keys beside it included. A key ending in `_set` keeps its own `400 readonly_setting`. Before 1.3.0 any key was stored, so a typo answered 204 and changed nothing (#421).

**Errors and limits worth knowing.** A handler panic answers the standard `500 internal_error` JSON envelope (logged with the request id and stack, never echoed), not an empty body. A `429 rate_limited` from the shared sixty-second window carries `Retry-After` as the whole seconds *left in that window* (rounded up, at least 1, at most 60); the per-account MFA lock keeps its fixed length. `POST /tickets/{id}/resolve` takes an optional body: empty resolves with no notes, malformed JSON is `400`. `POST /guest/resend` accepts an empty body as `{}`. The passkey finish routes read at most 64 KiB (`413 too_large`) and answer a fault writing the session as `500`, not `400 no_challenge`. `POST /me/mfa/enroll/confirm` answers a wrong code or an unstarted enrolment as `400 invalid_code` and a storage fault as `500` (`user.ErrMFAInvalidCode`, `user.ErrMFANotStarted`). A 405 on `/tickets` names its methods in `Allow` as one line per method (chi's behaviour); read every line (#421).

**Settings dump and choice-list settings.** Six settings take one value from a fixed list: `closed_reopen_policy`, `audit_mask_requester_names`, `attachment_scan_policy`, `attachment_infected_handling`, `attachment_mismatch_handling` and `attachment_reputation_refresh`. `PATCH /admin/settings` refuses any other value, `null` and a non-string included. `GET /admin/settings` reports each one as the value in force, not the stored bytes. A stored value the `PATCH` would refuse (only a direct database edit can put one there) is reported as what the server does with it instead: `off`, `everywhere`, `refuse`, `refuse` and `biweekly` respectively. The exception is `attachment_scan_policy`, which is reported as unset, because an unrecognised scan policy follows the scanner address exactly as an unset one does. Echoing the stored value made every save on every tab fail until someone changed that one dropdown, because the settings page sent every setting on each save ([#383](https://github.com/PubliciaLLC/go-help-desk/issues/383)). The page now sends only the settings the operator changed ([#394](https://github.com/PubliciaLLC/go-help-desk/issues/394)), so a damaged value of any kind blocks only a save that changes it. The dump still reports the value in force, for clients that send it back whole. An unset setting stays absent from the dump.

### MCP Interface

Exposes help desk operations as an MCP server for AI tool integration, over two transports behind one authentication chain:

- **Streamable HTTP at `/mcp`**, the current MCP transport. `/mcp/` answers the same, because the 1.2 documentation gave that URL. It is **stateless**: no `Mcp-Session-Id` is issued, every POST is one complete JSON-RPC exchange answered with `application/json`, `GET` answers `405` because the server never sends anything unprompted, and `DELETE` answers `405` because there is no session to end (mcp-go would otherwise unregister whatever session id it is given, an SSE stream's included). Stateless is deliberate. mcp-go's session-issuing mode keeps every initialized session in memory until the client deletes it, and its stateless GET stream shares one session between all callers. Nothing in this tool set needs a session.
- **HTTP+SSE at `/mcp/sse` (stream) and `/mcp/message` (posts)**, the transport MCP deprecated in its 2025-03-26 revision. It is kept so existing client configurations keep working.

**Tools**

| Tool | Who may call it | Notes |
|------|-----------------|-------|
| `get_ticket` | any signed-in user | By UUID or tracking number. Returns the reply thread and linked tickets; internal notes are omitted for reporting users. `include_replies` (default `true`): `false` returns the ticket alone, without replies or links. |
| `list_tickets` | any signed-in user | Optional `assignee_user_id`, `status_id`, `priority`, `category_id`, `q` filters. `limit` defaults to 20 and is clamped to 100; `offset` pages the full result set. |
| `list_categories` | any signed-in user | The Category/Type/Item tree, nested, for filling in CTI. Active entries only; `include_inactive: true` adds retired ones (the tool applies no role check to it, unlike the REST catalogue, which shows archived entries to staff and administrators only). |
| `list_statuses` | any signed-in user | The statuses this instance defines. |
| `create_ticket` | staff, admin | Category required; Type and Item optional. `reporter_user_id` or `reporter_email` (with `guest_name`, always required with the address) names the subject of the ticket; both are absent by default and the caller is the subject. `description` is Markdown unless `format: "plain"` is passed (#443); an invalid `format` is a tool error. The ticket is recorded as `mcp` (#130), including one filed for someone else; a `source_channel` argument is a tool error and nothing is written. |
| `add_reply` | staff, admin | A public reply emails the requester exactly as the REST reply does (#442): the guest's address (with a guest link) or the reporting account's, through one shared rule, `ticket.Service.ReplyRecipient`. No mail is sent when the requester has no address on record or the account is gone, or when the requester is a staff member or administrator writing the reply on a ticket they filed (they are not mailed a copy of their own reply, over REST or MCP; an ordinary requester replying on their own ticket still is); the reply is still saved. `internal: true` posts a staff-only note and never notifies the requester. There is no `notify_customer` option over MCP: a public reply always notifies. `body` is Markdown unless `format: "plain"` is passed (#443). |
| `assign_ticket` | staff, admin | To a user (`assignee_user_id`) or a group (`assignee_group_id`). To take the ticket off whoever has it, pass `clear_assignee: true` (the audit entry is `unassigned`); it cannot be combined with an assignee id, and omitting everything is refused rather than read as unassigning, because that silently took tickets off the people working them. |
| `update_ticket_status` | staff, admin | Target status must be one the caller's role may transition to. A ticket in Closed cannot be moved out of it unless `closed_reopen_policy` lets the caller's role (#349; off by default): the result says reopening is disabled or restricted and points at `create_follow_up`. There is no separate reopen tool and never was; this is MCP's path out of Closed, through the same service rule as REST. A move out of Resolved or Closed is a reopen like any other (flagged, priority raised on the first, SLA restarted, staff mailed), with the caller as the actor. |
| `create_follow_up` | staff, admin | `ticket_id` of a **Closed** ticket. Opens a new, linked ticket (see Closed is terminal); refused for a ticket that is not closed. |

**Authorization.** `/mcp` and everything under `/mcp/` run behind the same middleware chain as `/api/`, so
every call is authenticated. On both transports the acting identity is taken from each HTTP request, never from a stream or session: an SSE stream opened by one credential does not make later messages act as it, and a Streamable HTTP request acts as the credential it carries. Beyond that, the transport does not decide what a
caller may do: each tool gates its own writes, and every read is filtered
through the same visibility rule the REST API applies — staff scope when
enforcement is on, own-tickets-only for reporting users. A ticket the caller may
not see reports "not found" rather than "forbidden", so tracking numbers cannot
be probed over MCP.

The REST API answers the same way as of this release: `404 not found`, not
`403 forbidden`, for a ticket that exists and the caller may not see — the same
status, code and message `GET /tickets/{id}` already gives a tracking number
that does not exist at all, so a signed-in reporter can no longer walk the
sequential numbers (`GHD-2026-000001`, `...000002`) to learn which exist. This
was a breaking change to the REST API's published contract, made deliberately
in this beta's bug-fix branch rather than deferred to a major version — see
`ticketNotFound` in `backend/internal/server/ticket_access.go` and #174.

**Origin.** The MCP specification asks servers to validate the `Origin` header against DNS rebinding, and so does every MCP route: `/mcp`, `/mcp/`, `/mcp/sse` and `/mcp/message`. mcp-go does not, so `ProtectMCP` does it.

- A request with **no `Origin` header** passes. Only browsers send one on a cross-origin request, and MCP clients that are not browsers (SDKs, editors, `curl`) send none.
- A request with an `Origin` must carry exactly one, and it must be the origin of `BASE_URL`: scheme, host and port, compared without regard to case and with the default ports (80 for `http`, 443 for `https`) filled in. Anything else, `null` included, is refused with `403` and the error code `origin_not_allowed`.
- The comparison is strict. An `Origin` with a path, a trailing slash, userinfo, a percent-escape or any byte outside printable ASCII (browsers send hosts as punycode) is refused, as is a repeated header, because a browser sends none of those.
- The check runs **before authentication**. The refusal is the same for every caller, so it does not reveal whether a key is valid, whether a session owes MFA, or what role it has. A caller with no `Origin` never meets it and sees the responses it always saw.
- If `BASE_URL` has no usable origin, every request that carries an `Origin` is refused (fail closed). The server does not start without a usable host in `BASE_URL` (passkeys refuse it), but it does start on other unusable values, such as a scheme other than `http`/`https` or a port out of range, and those get this. A host written with non-ASCII characters is not one of them: passkeys refuse it too (the host is the relying-party ID, and go-webauthn rejects a non-ASCII one), so the server does not start. Write `BASE_URL` with the punycode host, which is also what a browser sends in `Origin`.
- The check applies to **every** request that reaches `ProtectMCP`: `/mcp`, `/mcp/`, and the older SSE transport (`/mcp/sse`, `/mcp/message`), and an unknown path under `/mcp/` as well, so a route added later is covered without anyone remembering to list it. The SSE transport is deprecated but not harmless: a browser page can open its stream and post to its message endpoint exactly as it can reach `/mcp`, and a DNS-rebound page reaches both alike, so leaving it open would leave the very hole the check closes on the other routes. An earlier draft of this release exempted SSE to keep existing configurations working; that was reversed (#460) because no legitimate client is affected: SDKs, editors and `curl` send no `Origin`, and a browser client served from `BASE_URL` itself sends the right one. `TestMCPOrigin_SSEIsChecked` pins it.

**SSE sessions are bound to their actor** (#495). The stream `GET /mcp/sse` records who opened it: the user and the credential it came in on (the API key, the OAuth client, or the browser session). A `POST /mcp/message` for that `sessionId` is processed only when its own authenticated actor is the same user on the same credential. Anyone else, an unknown or missing `sessionId`, and a stream that has closed all get the same `403` (`forbidden`, "session not available to this caller"), so the answer does not reveal which ids exist, and the body is not read. The same person on a different key is a different actor. The record is held in memory and dropped when the stream ends. Tests: `TestMCPSSE_SessionBoundToActor`, `TestOwnerOf`.

**Request body size** (#481). Every `POST` body on both transports (`/mcp`, `/mcp/`, `/mcp/message`) is capped at 1 MiB, the same as the REST API's JSON cap. A request is one JSON-RPC call and the largest arguments any tool takes are a ticket description or a reply body; MCP has no attachment tool. Over the cap the server answers `413` with `{"error":{"code":"request_too_large",...}}` and closes the connection; it reads no more than 1 MiB + 1 byte to decide, whether or not the client declared a length (before, a 100 MB body allocated 340 to 712 MB). `mcp.MaxRequestBody` is the constant. Tests: `TestMCPBodyLimit`, `TestHandler_OversizeBodyIs413WithJSONEnvelope`.

A browser-based MCP client served from an origin other than `BASE_URL` cannot use any MCP route. It could not before either: the MCP routes grant no CORS preflight, so the browser never sent the request.

The Origin check refuses any request that carries an `Origin` that is not `BASE_URL`'s, the stream `GET` included: a cross-origin `GET /mcp/sse` made with `EventSource` or a CORS-mode `fetch` carries one and is refused (`TestMCPOrigin_SSEIsChecked`). What it cannot stop is a stream `GET` that carries no `Origin`: a same-origin `GET` after DNS rebinding, or a no-cors or navigation `GET`. The other barriers do not all apply to every route.

- On `/mcp` the check is one of several independent barriers. A non-JSON `POST` is refused before anything runs, and a JSON `POST` needs a CORS preflight that `/mcp` never grants. Those two hold **only for `/mcp`**.
- On `/mcp/message` a `text/plain` `POST` runs, and it is a "simple" request, so no preflight is sent. For a page on the same site as the help desk (a sibling subdomain, say) the `SameSite=Lax` session cookie is sent with it, so **the Origin check is the only barrier there**. `TestMCPOrigin_SSEMessageCheckedWhereNothingElseStopsIt` pins that case.
- The stream `GET` (`/mcp/sse`) that carries no `Origin` is protected by authentication and by what the page can do with the answer. A no-cors `GET` from a sibling-site page carries the `SameSite=Lax` cookie and passes authentication, and is harmless only because the response is opaque to that page. A DNS-rebound page holds no credential: it gets no help-desk cookie and has no API key.

Tests: `TestOriginPolicy_Allows` (the comparison), `TestMCPOrigin_*` (the check behind `ProtectMCP`) and `TestMCPStreamable_NoCrossSiteSurface`, plus the cookie attributes in `backend/internal/database/sessionstore/cookie_attributes_test.go`.

### Authentication Methods

| Consumer | Auth Method | Details |
|----------|------------|---------|
| Browser (SPA) | Session cookies | HttpOnly cookie carrying an opaque id; the session itself is a row in Postgres. Backed by local auth, SAML or OIDC. |
| Formal integrations (JIRA, chatbots, CI) | OAuth2 client credentials | client_id + client_secret → short-lived JWT, scoped per integration. The client acts as its own service user (see [OAuth Clients and Service Users](#oauth-clients-and-service-users)) |
| Lightweight scripting / webhooks | API keys | Hashed bearer tokens with scoped permissions |
| MCP | Inherits from above | Served at `/mcp` (Streamable HTTP) and `/mcp/sse` + `/mcp/message` (SSE), behind the same authentication chain as `/api/`, and scopes are enforced per tool. It calls the domain services directly, not the REST API, so a rule that lives in a REST handler does not apply to it unless the tool repeats it (for example, `create_ticket` does not run auto-assignment). |

Sessions are server-side rows, not self-contained cookies, because that is the
only shape in which a session can be revoked. Logout, a password change, an MFA
reset, a role change, disabling and deleting all take effect on the next
request. A session whose owner is disabled or deleted stops loading whether or
not anything deleted it, and the session id rotates on login and on any
privilege change. Lifetime is 7 days. SAML's library cookie is not a
session; it is spent at hand-over (see Authentication → SAML).

**The generated JWT signing key is stored unencrypted in `instance_secrets`,
on purpose (#454).** When `JWT_SECRET` is unset or under 32 bytes, the server
generates a random 32-byte key once and keeps it in that table. Do not
"fix" this by encrypting it:

- It sits in the same database as session tokens, which are more sensitive: a
  reader of this table could already act as any signed-in user.
- Encrypting it with `SESSION_SECRET` would lock the key out whenever that secret
  changed, and the key would silently regenerate. Every OAuth client would then
  hold a token the server no longer accepts.

A long `JWT_SECRET` in the environment still wins over the stored key, with
one exception: a value that is exactly one of the example secrets this
repository publishes (`config.IsExampleSecret`) is discarded and treated as
unset, with one warning logged at startup. Anyone can read the example, so a
token signed with it would prove nothing. `SESSION_SECRET` has no such fallback
and is still flagged, not discarded. A long `JWT_SECRET` that merely looks like a
placeholder without being an exact example is used (it is the key), so it is
flagged in the admin warnings like `SESSION_SECRET`.

**An unwritable `ATTACHMENT_DIR` disables uploads and nothing else (#458).**
The directory is probed (a scratch file is created and removed) at startup,
which logs a warning, and again on every upload attempt and every
`GET /admin/security-warnings` (`attachment_storage`). It is probed rather than
remembered so that fixing the permissions recovers on the next request with no
restart and no timer. While it fails, the staff, requester and guest upload
routes answer 503 `attachments_unavailable` before reading the body, the admin
banner says why, and downloads of files already stored keep working for as long
as the directory is readable.

### Credential Scopes

Machine credentials — API keys and OAuth clients — carry scopes. Browser
sessions do not: a session **is** the user, with whatever their role allows.
Scopes exist to give a machine credential *less* than its owner, and there is
nothing to narrow when a person is driving.

A scope is `resource:action`, where action is `read` or `write`. `audit` has
only `read`.

| Resource | Covers |
|----------|--------|
| `tickets` | Tickets and everything under `/tickets/{id}` — replies, links, tags, attachments, custom fields, status transitions — plus the staff picker (`/staff`) |
| `audit` | The admin-wide audit view (`/admin/audit`). A session needs no scope; an API key or OAuth client needs `audit:read`, which an administrator grants on purpose and which `tickets:read` does not imply. There is no `audit:write`: only the running server writes audit entries, so granting it to an API key or OAuth client is refused like an unknown scope, and one stored before the refusal ([#371](https://github.com/PubliciaLLC/go-help-desk/issues/371)) grants nothing, not even `audit:read` |
| `users` | Reading users, and the requester lookup (`GET /users/lookup`, #444). **Read only for keys:** keys can't write users, so `users:write` is no longer offered by the catalogue or the pickers (it still parses, so a stored key or a script that names it keeps working, and it opens nothing) |
| `groups` | Groups, their members, and their category/type scopes. `groups:write` covers creating, renaming and deleting groups; **membership and scope writes are session-only** (keys can't write users), so it opens neither |
| `categories` | Categories, types, items, and custom-field assignments |
| `tags` | Tag administration |
| `canned_responses` | Canned response templates |
| `sla` | SLA policies |
| `settings` | Instance settings, statuses, and SAML/OIDC configuration |
| `plugins` | Plugin administration |
| `webhooks` | Webhook subscriptions |
| `credentials` | API keys and OAuth clients |

Four rules govern them:

1. **Scopes narrow; they never grant.** The scope check runs *after* the role
   check, so `users:write` on a key owned by a reporting user reaches nothing.
   An API key acts at its owner's role; an OAuth client acts at its service
   user's role (staff or user, never admin; see
   [OAuth Clients and Service Users](#oauth-clients-and-service-users)).
2. **An empty scope list denies everything.** A credential with no scopes
   reaches nothing at all.
3. **Write implies read** on the same resource, except `audit`, which has no
   write scope. An integration that may create tickets but not read them back
   is not a useful shape.
4. **There is no wildcard.** A credential that should reach everything lists
   every scope it needs. This keeps what a credential can do legible from the
   credential itself, and means adding a resource later does not silently widen
   credentials that already exist.

The action is taken from the HTTP method — `GET` and `HEAD` need `read`,
everything else needs `write` — and enforced per route group rather than per
handler, so a route added later cannot forget it.

**MCP is covered too.** Every MCP message is a POST, so the action cannot come
from the method; each tool declares what it needs at registration. The read
tools (`get_ticket`, `list_tickets`, `list_categories`, `list_statuses`) need
`tickets:read` and the write tools (`create_ticket`, `add_reply`,
`assign_ticket`, `update_ticket_status`, `create_follow_up`) need `tickets:write`. The two reference
tools sit under `tickets` rather than `categories`/`settings` because they exist
to compose a ticket and are available to every role, unlike the administrative
category and status endpoints. The top-level `/tags` and `/statuses` reference
endpoints are covered by `tickets:read` for the same reason.

A machine credential is also refused the endpoints that change how an account
authenticates: its own (`/me/password`, `/me/mfa/enroll*`, `/me/passkeys*`) and its owner's API keys (`/me/api-keys*`, #450).

**Keys can't write users** (Erik, 2026-10-16). An API key or OAuth client may
**read** users (`GET /admin/users`, `GET /admin/users/{id}`, the group member and
scope lists, the assignee picker) and nothing more. **Every user write needs a
signed-in administrator.** Each of these carries `DenyMachineCredentials` (an API
key gets `403 session_required`; an OAuth client is a staff identity, so the
admin role check refuses it first with `403 forbidden`):

| Route | Writes |
|---|---|
| `POST /admin/users` | creates an account |
| `PATCH /admin/users/{id}` | role, disabled, name, email, MFA reset |
| `DELETE /admin/users/{id}` | soft-deletes an account |
| `POST /admin/users/{id}/password` | sets a password |
| `POST /admin/groups/{id}/members`, `DELETE /admin/groups/{id}/members/{userId}` | group membership, which grants ticket visibility |
| `POST /admin/groups/{id}/scopes`, `DELETE /admin/groups/{id}/scopes` | what a group's members can see |

The rule is the route, not the field. It replaces a set of per-field rules (no
administrators, no password on create, no `reset_mfa`, no email) that each closed
one door of the same room and left another: a key could create a staff account
with a password it chose, or promote a requester whose password it knew, sign in
as that person (no second factor), and from that session create an API key
(`POST /me/api-keys`) that survives the revocation of the key that started it;
re-addressing an account would send its reset link (#451) to whoever holds the
key. A key that can write users can make itself a person, so it cannot write
them. Group create, rename and delete, and the rest of the admin surface, are
unchanged.

What this does not change, and is not a hole this rule closes: **OIDC sign-in
can adopt an existing account by email** (`canAdoptByEmail`): when a verified
address matches an account that is active, not a service account, not an administrator, not federated
through SAML, not a service account and not bound to another OIDC subject, the first OIDC sign-in with
that address takes it over, whatever its password. That is how a locally
provisioned account joins an identity provider, and it is how this behaved before
#450. With keys unable to create or re-address accounts, a key can no longer
create the account that would be adopted, but a signed-in administrator can, and
so can self-signup. The adoption is gated by the identity provider verifying the
address, and by `allowed_email_domains` (Authentication, OIDC).

Four more things are off limits to a machine credential, for the same reason:

- **Changing the SAML or OIDC configuration.** Login providers are settable from
  the admin UI and nowhere else. Repointing the identity provider at one the
  caller controls, then asserting a federated administrator's subject, yields an
  administrator session — and it is also the last way a credential could make
  the server fetch a URL of the caller's choosing on the internal network.
  Blocked on both doors: the dedicated config routes and the `saml_*` / `oidc_*`
  settings keys. Reading the configuration stays available to automation, since
  the handlers already blank the secrets.
- **Changing an auth-critical setting** (`admin.AuthCriticalKeys()`; refused
  with `403 session_required`). These decide who may authenticate and how, or
  what the instance will accept and keep: MFA enablement and enforcement; the
  SAML and OIDC keys; the email-domain allowlist and the signup toggles; guest
  submission (#177: it decides not just whether anonymous people can file a
  ticket but also, since the category catalogue stopped being anonymous,
  whether that catalogue is readable without a session at all, one flag and two
  exposures); `closed_reopen_policy` (#349); the attachment policy (scanner
  address and policy, allowed types, infected and mismatch handling, the
  reputation toggles, keys and refresh interval); and audit retention, staff
  visibility of change history and whether requester names are masked in audit
  views; who may create API keys (`staff_can_create_api_keys`) and how long a new one may live (`api_key_max_lifetime_years`, #450); and who sees tickets and who is handed them (`ticket_scope_enforced`, `auto_assign_group_id`, `auto_assign_user_ids`, #450): a key that could switch scope enforcement off, or point auto-assign at an account it controls, would widen what that account sees or is given.
  Ordinary configuration such as the site name
  stays automatable.
- **Verifying a second factor** (`POST /auth/local/mfa/verify` and
  `POST /auth/local/passkey/start|finish`). A machine credential reaching the
  code check could spend the account's durable failed-attempt budget and lock
  the owner out repeatedly; the passkey routes sit beside it and answer to the
  same rule.
- **Creating an API key** (#450). Keys can't make keys: only a signed-in person
  creates one. (It used to be allowed within the key's own scopes, which still
  let `credentials:write` mint a successor before the first expired, and that one
  another.) See Who can create API keys, below. A key can still list and revoke
  keys.

Scopes cannot express any of this. The narrowest possible key still belongs to
its owner, so any scope reaching those routes reaches account takeover.

`GET /api/v1/admin/scopes` returns the catalogue. The admin UI builds its
picker from it so the two cannot drift.

**Who lists and revokes which API keys** ([#419](https://github.com/PubliciaLLC/go-help-desk/issues/419),
[#466](https://github.com/PubliciaLLC/go-help-desk/pull/466)). An administrator
signed in through the browser lists and revokes every key, with its owner
shown (`owner_name`, `owner_email`). An API key lists and revokes only
itself: not its owner's other keys, and not anyone else's (any other id answers
`404`, as if it did not exist). The rule follows the credential, not the role,
so a leaked key held by an administrator cannot revoke the owner's other keys,
which are what would let the owner recover.
Before 1.3, revoking ignored the owner while listing did not, so any admin
credential that knew a key id could revoke another administrator's key.

**Who can create API keys** ([#450](https://github.com/PubliciaLLC/go-help-desk/issues/450)).
A key is a long-lived bearer credential that acts as its owner, so who may make
one is a permission decision with one switch and no per-user exceptions.

- **Administrators** can always create their own keys.
- **Staff** can create their own only while the admin setting
  `staff_can_create_api_keys` ("Staff may create their own API keys", Admin →
  Settings → Authentication → API keys) is on. It is off by default. It governs
  *creating* a key: turning it off revokes nothing, and staff can still list
  and revoke their own keys whatever it says, including a key an administrator
  issued for them. Revoking only ever removes access, and a person must be able
  to remove a credential that acts as them.
- **Requesters never own keys.** `/me/api-keys` answers them `403`, and an
  administrator cannot issue one for them either (`400 requester_cannot_own_key`).
  A requester's integration uses an OAuth client or a staff member's key.
- **An administrator can issue a key for a staff member, or for themselves**
  (`user_id` on `POST /admin/api-keys`, a signed-in session only, or `user_id`
  equal to their own id). The key belongs to that account and acts as it. Not for
  another administrator (`400 cannot_issue_for_administrator`: one administrator
  minting another's credential), not for a requester, and not for a service
  account, the identity an OAuth client acts as (`400
  service_account_cannot_own_key`; the client has its own credential and the
  admin "issue for" search leaves service accounts out). A deleted account is
  `404`, a disabled one `400 user_disabled`. The owner is judged before the
  scopes, so the refusal gives the real reason.
- **Keys can't make keys** (Erik, 2026-10-16). Only a signed-in person creates an
  API key, on `/me/api-keys` or `POST /admin/api-keys`. A key that could would
  mint a successor that expires up to the full lifetime limit later, and that
  one another, so the limit would mean nothing, and revoking the first would
  remove none of the rest. `GET/POST /me/api-keys`, `GET /me/api-keys/scopes` and
  `DELETE /me/api-keys/{id}` sit inside `meRouter` behind
  `DenyMachineCredentials`, `RequireMFA` and `RequireRole(admin, staff)`;
  `POST /admin/api-keys` carries `DenyMachineCredentials` too. An administrator's
  API key gets `403 session_required` on both; an OAuth client is a staff
  identity, so on the admin route the role check refuses it first (`403
  forbidden`). Listing and revoking stay open to a key on the admin routes (a key
  lists and revokes only itself, #466). This also makes the "a machine
  credential naming another user" and "broader than itself" cases unreachable,
  so those checks are gone rather than left as dead code.
  **OAuth clients are covered by the same rule** ("API keys cannot create oauth",
  Erik, 2026-10-16): `POST /admin/oauth-clients` carries
  `DenyMachineCredentials` (an API key `403 session_required`, an OAuth client `403
  forbidden`). A client made by a leaked key would outlive it. A key can still
  list and delete clients. There is no route that rotates a client's secret, so
  creation is the only path that mints one.
- **A webhook dies with the key that made it** (Erik, 2026-10-16). A key may
  still create and edit webhooks. The hook records the API key that created it
  or last changed its URL (`webhook_configs.api_key_id`, no foreign key, so
  deleting the key does not forget it); a hook made or last re-pointed in a
  session has none. When that key is no longer active (revoked, expired, or its owner disabled or
  deleted), the hook is
  **paused**: `paused: true` and `paused_reason` ("Its API key, or that key's
  owner, is no longer active") on every read, and the dispatcher sends it nothing. It is
  judged from the key's own row on every read and at every delivery attempt, so
  an expiry pauses a hook with no job running, a queued delivery is settled
  rather than sent, and retries stop. The link is a plain column with **no
  foreign key**, so deleting the key cannot null it and make the hook look
  session-made: a missing key counts the same as a revoked one, and so does a
  key whose owner is disabled or deleted (it no longer authenticates). Every one
  of those fails closed. A key cannot lift the pause by editing: any `PATCH` of a
  paused hook with a key is `403 webhook_paused` (re-pointing it would tie it to
  the editing key and un-pause it unseen); a key may still delete it. Changing a hook's URL in a session is an
  approval of that destination, by a person who typed it, so it clears the link;
  any other session edit (events, filter, enabled) does not, and a paused hook
  stays paused until it is re-approved explicitly. A key changing something else
  leaves the link as it was. `POST /admin/webhooks/{id}/reapprove` (session only) clears the link,
  and the hook fires again; the Webhooks page shows a Paused badge with the
  reason and a Re-approve button. Nothing is written to the delivery history for
  a skipped delivery, as for a disabled hook; #518's dropped-delivery record was
  not merged when this was built, so this does not use it.
- **A change of role keeps the person's keys** (Erik, 2026-10-16). A key acts
  within its owner's *current* role: the role check runs on every request, and
  the scope table only limits what can be put on a key at creation. Demoting an
  administrator leaves their keys alive but able to reach only what staff can.
  Promoting a staff member leaves their keys alive and, by the same rule, able to
  reach what an administrator can reach, so "issue a key for a staff member, then
  promote them" leaves an administrator holding a key another administrator
  issued. That is an accepted consequence, not an oversight; revoke the keys, or
  use the password-reset checkbox, if it is not wanted.
- **`staff_can_create_api_keys` is auth-critical** (`admin.AuthCriticalKeys`), so
  a machine credential cannot change it. Switching it on widens who may hold a
  bearer credential, the same kind of change as `closed_reopen_policy`, and a
  leaked administrator key must not be able to switch it on and then use it.
  (The alternative, session-gated detail for settings that only widen what staff
  *see*, is the `staff_can_view_*` family. This one widens what staff can
  *make*, so it takes the stricter reading.)
- **A key's scopes never exceed its owner's role.** `auth.ScopesForRole` is the
  table: an administrator may carry any scope; staff `tickets:read|write`,
  `groups:read` and `audit:read` (not `users:read`: the one staff route that needed it, the requester lookup, is session-only since #290); a requester none, since requesters never own a
  key (an existing requester key still acts within the role check). It is checked
  against the key's *owner*, so an administrator issuing a key for a staff member is
  limited by what staff can do; a scope outside it is `400 invalid_scope`.
  `GET /admin/scopes?role=staff` serves that list, and the admin picker offers
  only it once an owner is chosen.
  Scopes only narrow, so this is defence in depth and not the boundary: the role
  check still refuses a staff key every admin route. What it prevents is a key
  that looks as though it can edit users and cannot. The table is written out
  and `TestScopesForRole_MatchTheRouter` derives the same answer from the
  router's own middleware for administrators and staff, fails if an
  administrator-only scope is offered to staff, and fails if a scope the router lets a role use is
  missing from the table.
- **Every issue and revoke is audited**, a person's own keys and keys issued or
  revoked on someone's behalf alike (`entity_type` `api_key`; actions
  `api_key_issued` and `api_key_revoked`; the entry carries the owner's id,
  `on_behalf`, the scopes and the expiry, and never the key's name, which is
  free text). A revoke-on-reset is a different entry: `entity_type` `user` (the
  account whose keys went), action `api_keys_revoked`, with `keys_revoked`, the
  count. The write is best effort and detached from the request with
  `context.WithoutCancel`, like `user.Service.ResetMFA`: an audit entry here does
  not gate anything, and hanging up after the key is written cannot suppress it
  (`TestAPIKeyAudit_IsWrittenEvenIfTheRequestWasCancelled`).
- `api_keys.created_by` (migration 000050) records who issued each new key. It
  equals `user_id` when the owner made it and differs when it was issued on their
  behalf; NULL means unknown (a key made before 1.3, or an issuer deleted since).
  It is returned by both key lists, and the Account page says "Issued for you by
  an administrator" and the admin page "Issued on their behalf".
- **Maximum key lifetime** (`api_key_max_lifetime_years`, Admin → Settings →
  Authentication → API keys). Every key made through `/me/api-keys` or
  `POST /admin/api-keys`, own or on someone's behalf, must expire, and no later
  than this: **2 years by default**, set on a slider with six stops, 1 to 5 years
  and Never. Stored as the integer 1 to 5, or `-1` for Never; `0`, `6`, a
  fraction, a string and null are refused with 400, and an unreadable stored
  value reads as 2. With a limit, a missing expiry is `400 expires_at_required`
  and one past it `400 expires_at_too_far`, naming the limit; only with Never may
  `expires_at` be omitted. The setting is **auth-critical** (session only),
  because raising it, or choosing Never, widens how long a stolen key stays
  useful; the panel shows a red warning whenever it is above 2. The check is made
  **when the key is created, never when it is used**. A key that already exists
  with no expiry, or an expiry beyond the current limit (after the limit is
  lowered), keeps working and is returned with `beyond_limit: true` in both key
  lists, which the screens show as "No expiry (longer than the current limit)" so
  it can be rotated. The create forms default the date to the longest allowed,
  require one while there is a limit, and refuse a later one; the server enforces
  the same. The UI sends the end of the chosen day, but never later than an hour
  before the limit, so the last day the form offers is not refused.
- **An administrator's password reset can revoke the person's keys.** The reset
  form has "Also revoke this person's API keys", unticked; `revoke_api_keys` on
  `POST /admin/users/{id}/password`. When true the server deletes every key the
  account owns after the password is set and the sessions are ended, and records
  `api_keys_revoked` on the account's audit entries with `keys_revoked`, the
  count. It is a separate step from the reset, not one transaction, and it is
  detached from the request (`context.WithoutCancel`) like the audit write. If
  the revoke fails after the reset succeeded, the answer is `500 keys_not_revoked`
  with "The password was reset, but the API keys were NOT revoked.", which the
  page shows as it is. A person
  changing or resetting their own password never touches keys, and neither
  `server unlock` nor `server reset-factors` does.
- Both screens take the expiry as a date. `GET /me` reports `can_create_api_keys`
  and `api_key_max_lifetime_years` so the Account page knows whether to offer the
  form and what to cap it at.

**A blank webhook `secret` clears it, by design.** `PATCH /api/v1/admin/webhooks/{id}`
with `"secret": ""` removes the stored secret, and deliveries to that hook are
no longer signed; an absent or `null` secret keeps it. This is a deliberate
choice and stays admin-only: the admin router needs the administrator role and
a second factor, and an OAuth client carries the staff role, so no credential
below an administrator can reach it. It is revisited if that stops being true.

### Other protections

Things the code does that are not obvious from the feature list, recorded here
so they are not removed as dead weight:

- **Credential throttling.** Failed password attempts are counted per account,
  not per source address — an address-keyed limit is defeated by any proxy or a
  forged forwarding header. The password counter is in-process, so a restart
  clears it and N replicas multiply the budget by N; `AUTH_RATE_LIMIT_PER_MINUTE=0`
  disables it and the signup limit with it. The MFA failed-attempt count is on
  the user row and does survive a restart.
- **Ticket list paging.** `GET /tickets` takes `?limit=` (default 100, maximum
  200) and `?offset=`. Offset-based, not page-based. The staff view merges the
  caller's own tickets with each of their groups', so it reads each source to
  the end of the requested page and slices after merging — pushing the window
  into each query returns limit × (1 + groups) rows.
- **Uploaded images are capped by what decoding them will cost**, checked from
  the header before any decode. A byte-size limit is not a memory limit:
  compressed formats expand, and a 169 KB PNG decodes to 142 MB. Two bounds
  apply: twenty-five megapixels, and 100 MB of decoder allocation, which is not
  the picture's size (a progressive JPEG holds every DCT coefficient until the
  image is reconstructed, and a CMYK or RGB one decodes through a second
  full-resolution image, so "pixels times four bytes" let a 214 KB file cost
  403 MB). Because every narrower rule of the form "know each shape" was beaten
  by a differently-shaped file, the rule is a refusal: **a JPEG whose header
  cannot be read is refused** rather than falling back to a weaker estimate.
  Every real JPEG parses, and "I cannot tell how much this will cost" is a
  reason to refuse. That converts the next gap in the estimate from a way
  through into a refusal, which is worth more than any single thing the
  estimate knows. A limit on how many images are decoded at once bounds the
  process rather than the request.
- **Security headers** on every response: a content security policy, `nosniff`,
  `X-Frame-Options: DENY` and a referrer policy. The uploaded logo is served
  with a stricter, sandboxed policy, so a file that got past the upload check
  still cannot execute.
- **Webhook targets are address-checked** at the moment of connection, so a
  hostname resolving to an internal address, a redirect to one, and DNS
  rebinding are all refused. The SAML metadata and OIDC issuer URLs are
  deliberately *not* address-checked — a self-hosted identity provider on a
  private network is a normal topology — and are restricted by who may set them
  instead.
- **Webhook payloads never carry the text of an internal note.** A staff-only
  note is its own event, `ticket.note_added`
  ([#448](https://github.com/PubliciaLLC/go-help-desk/issues/448)), so a hook
  subscribed to `ticket.replied` is never sent one and the event name says
  which it is. Before, it received the text of every internal note and could
  not tell them apart.
- **Chat/ITSM payload formats escape mentions and never carry an internal
  note body.** Slack escapes `&`, `<` and `>` in user-chosen text, so a
  subject of `<!channel>` cannot page a channel; Discord always sends
  `allowed_mentions: {"parse": []}`, so `@everyone`-style text cannot ping a
  server. All four formats (Slack, Teams, Discord, JIRA) are built from the
  same summary the raw format's internal-note omission already produces, so
  an internal note's body reaches none of them, not just the raw payload.

**Scopes are enforced** (since 1.2.0). Before it they were accepted, stored and
returned by the API and no code read them, so every credential issued as
restricted was unrestricted. Credentials created before enforcement carry no
scopes and are therefore denied; they must be re-issued.

---

## Plugin Infrastructure (v2)

Deferred from v1; 1.x has no working plugin system. The data model and an
admin routes exist (`internal/domain/plugin/`, `handler_admin_plugins.go`;
there is no admin page), but nothing registers a plugin, so the list is always
empty and enable/disable fails for every id. None of the capabilities below
are wired up: install and uninstall return `501 not_implemented`, no WASM runtime is linked
in, and `plugin.Registry.Dispatch` is never called from the ticket lifecycle,
so no plugin receives events. Treat this section as the
target design for v2, not a description of what ships today.

External chat/ITSM notifications (Slack, Teams, Discord, JIRA) do **not** wait
for this — see Notifications below, where they ship in v1 as payload formats
on the existing webhook feature instead of as plugins.

### Capabilities (v2)

- React to **ticket lifecycle events** (created, assigned, status changed, resolved, etc.)
- Add **custom fields / UI panels** to tickets
- Integrate **external systems** generically, beyond what a webhook payload format can express

### Theming

- **CSS/branding only** — logo, colors, fonts configurable in admin UI
- Not full layout-level theming

### Trust Model

- Both **1st-party and 3rd-party** plugins supported
- 3rd-party plugins run sandboxed with a restricted API surface

### Distribution

| Version | Method |
|---------|--------|
| v2 | Install/manage via **admin UI** (upload or URL) |
| v4 | **Plugin registry** for discovery and installation |

---

## Notifications (v1)

- **Email** — a reply on a ticket, to the reporter. A guest additionally gets
  the acknowledgement, and a note on each status change, resolution and reopen,
  because each of those replaces the link they hold and the mail is how the
  replacement reaches them.
- **Staff email** ([#441](https://github.com/PubliciaLLC/go-help-desk/issues/441),
  parts C and D of #464). Staff are emailed about their own work, not just
  requesters about theirs. Two events: a ticket is **assigned**, and the
  **requester replies** (the public reply of whoever filed the ticket: an
  account holder, a guest, or a staff member replying on a ticket they filed
  themselves; any other staff reply, or an internal note, is not one). A third,
  a **reopen** ([#464](https://github.com/PubliciaLLC/go-help-desk/issues/464)),
  is described below.

  Three per-user switches, all **on by default** (columns on `users`, set
  from **Your account → Email about tickets**, `PATCH /me/email-preferences`):

  | Switch | Mailed when |
  | --- | --- |
  | Tickets assigned to me | a ticket is assigned to me |
  | Activity on tickets I'm assigned to or follow | a requester replies on a ticket assigned to me, a ticket assigned to me is **reopened**, and a ticket I **follow** is assigned, gets a requester's reply or is reopened |
  | My groups' tickets | a ticket is assigned to a group I belong to, a requester replies on a group ticket that **nobody personally holds**, and a ticket in my group that nobody personally holds is **reopened** |

  A requester's reply on a ticket that has both an assignee and a group goes
  to the assignee only: the group's members would otherwise get mail for every
  reply on work a colleague already holds. Assignment mails both. Group
  members are told it is their group's ticket ("assigned to your group"), not
  that it is theirs. **Followers** (Ticket Followers, above) are mailed for both
  events under the second switch and its override, and are told it is a ticket
  they follow. **Followers who are the assignee, or members of the assigned
  group when the group itself is mailed for that event, get no follower mail**
  (Erik, 2026-10-14: they follow the ticket from a practical point of view).
  The group is mailed for an assignment, and for a reply on a ticket nobody
  holds; for those events a member's own group switch decides, and following
  on top does not bring back mail they turned off. The assignee is always left
  out of follower mail, and their own switches decide. For a reply on a ticket
  somebody holds, the group is not mailed, so a member who follows it is
  mailed as a follower. This is decided when the worker first picks up the change, a moment after the commit, not on the request. At send time
  only the follow row (and visibility) is checked, so unfollowing stops mail
  already queued, and mail queued to a follower is not lost if they are then
  assigned the ticket.

  **A reopen** (any door: Ticket Lifecycle) follows the reply rule (Erik,
  2026-10-14). The assignee is mailed, under the *requester replied* switch (the
  second one). The assigned group's members are mailed, under the third
  switch, **only while nobody personally holds the ticket**. Followers are
  mailed whoever holds it, under the second switch. So on a held ticket a group
  member hears about a reopen only if they follow it, as a follower; on an
  unheld ticket the group is mailed, a member who also follows gets exactly one
  mail, and their own group switch decides. The assignee never gets follower
  mail. Everyone gets one mail. Whoever did the reopening is not mailed, whether a requester (who is no staff candidate
  anyway), the assignee, a group member or a follower. The mail is built from
  the change's one event, which carries the kind `ticket.reopened`, who holds
  the ticket and who acted; a requester's reply that reopens is one change
  carrying both the reply and the reopen, and each person gets one mail for it
  (the first of assigned, reopened, requester replied whose switch is on). At send time the same checks run
  as for every staff mail: the person must still hold the ticket, still be in
  the group, or still follow it and be able to see it. The requester's own mail
  is unchanged: a force-reopen mails the guest, and a status change mails the
  guest through its own event.

  An administrator can **override each switch for everyone**
  (**Admin → Settings → Email**; the settings `staff_email_assigned_override`,
  `staff_email_activity_override`, `staff_email_group_override`): `user`
  leaves it to each person (the default), `on` and `off` ignore what people
  chose. An unrecognised stored value reads as `user`, never as a forced one.
  `GET /me` reports, for each switch, whether it applies to the person and
  whether an override forces it, and the account page shows a forced switch
  locked with "Set by your administrator".

  **An upgraded install starts with staff email off.** Migration `000052`
  stores all three overrides as `off` when the users table already holds a
  person (a service account does not count), so a 1.2 install that already has
  SMTP configured does not begin mailing staff on first boot. A fresh install
  migrates with no users, gets no stored override, and the defaults apply. An
  override already stored is kept (`ON CONFLICT DO NOTHING`). The down step is a
  no-op: it cannot tell its rows from an administrator's own `off`, and deleting
  them would turn mail back on. The administrator turns staff email on under
  **Admin → Settings → Email**.

  The rules that hold whatever the switches say:
  - **Nobody is mailed about their own action.** The person who made the
    assignment is skipped even when an override forces the switch on.
  - **One email per person per event.** Someone who is both the assignee and
    a member of the assigned group gets one, if either switch lets it through.
    That holds for a reopen too.
    A follower who is neither is mailed as a follower.
  - **Only people who can receive it**: an active staff member or
    administrator with an address. A requester account is never sent staff
    mail. Service accounts (the identity an OAuth client acts as, #490) never
    are: `user.User.CanReceiveStaffMail` refuses them, and the query that lists a
    group's mailable members leaves them out too.

  Staff mail uses the same no-content templates as requester mail (the
  tracking number and a link to the ticket behind the sign-in, nothing a
  requester wrote). Each person gets their own outbox row on the `email`
  channel, so one person's failing mail never resends anyone else's and none
  of it touches the requester's own mail. The row names the person, not an
  address: the address is read when the mail is sent, and mail queued for an
  account that was disabled or deleted in the meantime is dropped. The ticket
  is checked again too: mail is dropped if the person no longer holds the
  ticket, is no longer in the group that does, or (for a follower) can no
  longer see it under the ticket-scope rule (#391), because a queued row can
  wait up to an hour of retries while the ticket moves. Rows are
  made by the outbox worker (`notify.ChangeSplitter`, using `notify.StaffFanout`)
  when it first picks up the change's one email row, not on the request, and are
  never webhook events. A person qualifying for several kinds in one change gets
  one mail, and mail is deduplicated by recipient address; see Ticket Events:
  One Change, One Event.
- Email is a notification, not a copy of the ticket. A message says what
  happened, names the ticket by its tracking number, and links to it. It does
  not carry the ticket subject or the reply text, and the recipient's own
  address is written bare, with no display name.

  This is deliberate. Mail leaving the help desk is sent from the operator's
  domain, so anything in it is said with the operator's reputation behind it,
  and anyone who can file a ticket chooses that text. Recipients read the
  content in the application, where the existing access rules apply to it.

  **Guest tickets** carry a per-ticket link instead of a ticket id, so a
  recipient with no account reaches their own thread and nothing else.
- **Webhooks** — configurable HTTP callbacks for ticket lifecycle events:
  `ticket.created`, `ticket.assigned`, `ticket.status_changed`,
  `ticket.replied` (a customer-visible reply only), `ticket.note_added` (a
  staff-only note), `ticket.resolved`, `ticket.closed`, `ticket.reopened` (every reopen: a requester's reply, a status change out of Resolved or Closed, and `POST /reopen`) and
  `ticket.linked`, `ticket.priority_changed` (a priority change) and
  `ticket.classified` (a category, type or item change). A hook matches a change
  when any of the change's kinds is in its list, and fires once. The raw body
  keeps its shape. See Ticket Events: One Change, One Event.
  **What the raw format carries** ([#448](https://github.com/PubliciaLLC/go-help-desk/issues/448)).
  The raw body is **ids only**: the event name (`Type`), `TicketID`, `ActorID`
  (null for a system event, and for a routed create under `ticket.assigned`), `OccurredAt` and a `Payload` holding only the ids
  `new_status_id` (status changed) and `target_id` (linked), where the event
  has them. Every other event has no payload. Whether an event is a reply or a
  note is its name, so there is no `internal` flag, and no link type is sent.
  No address, subject, tracking number, priority, status name or reply text is
  sent, and none is stored with it. The keys are an allow list, so a key added
  to an event later stays off the wire until someone decides a receiver should
  have it. A receiver that needs the ticket reads it through the REST API using
  `TicketID`. **This changed from earlier 1.3 betas**, which also sent
  `TrackingNumber`, `Subject`, `Priority`, `guest_email`, `ReplyBody` and
  `reporter_email` in `Payload`. The Slack, Teams, Discord and JIRA formats are
  not affected: they are built from the event as the server holds it, not from
  the raw body, and keep their subject, tracking number, status and reply
  text (never a note's text, and never an address), including for
  `ticket.note_added`.
  Administrators manage them under **Admin → Webhooks** (URL, payload format,
  events, ticket filter, enabled — also when creating — edit, delete). A secret is write-only:
  it signs deliveries and is never returned by the API or shown again, and
  leaving the field empty on edit keeps the stored one. The list shows each
  hook's most recent delivery: when it started, the HTTP status, and on failure
  a class (`http_status`, `timeout`, `dns`, `tls`, `blocked_address`,
  `connection`, `other`). No error text is stored, because a URL can carry a
  token and a response body can carry anything; the details are in the server
  log. The latest-started attempt wins, and changing a hook's URL clears its
  result.
  **Headers.** Every delivery carries `X-GHD-Delivery`, a UUID that is the same
  on every retry of that delivery, and `X-GHD-Event`, the event name. Receivers
  can see an event more than once (below), and de-duplicate on the first.
  **History and Redeliver.** Each hook keeps its last 25 deliveries: the event
  name, when the first and latest attempts started, the HTTP status, a result
  (`ok` or a class), the number of attempts and whether another is pending.
  No payload and no error text is shown or returned. The event is kept beside
  each entry only so **Redeliver** can queue it again, as a new delivery with a
  new `X-GHD-Delivery`. What is kept is ids only: the ids of a raw body plus
  the link type and the id of the reply the event announced. No address,
  subject, tracking number or text. For a raw hook the replay is identical to
  the original. For a Slack, Teams, Discord or JIRA hook the message is
  **rebuilt from the ticket as it is now**: its current subject, tracking
  number and status name, and, for a reply, the reply text only if that reply
  still exists and is public (visibility is read at that moment; an internal or
  deleted reply is never sent, and a note never has text). If the ticket is
  gone the message is its headline only. So a replayed message can differ from
  the original, and a status-change message names the status the ticket has
  now. Redeliver is refused with
  `409 webhook_disabled` for a disabled hook,
  `409 webhook_not_subscribed` when the hook no longer lists that event, and
  `409 webhook_filter_excludes` when the hook's ticket filter, as it is now,
  does not admit the ticket, and `409 webhook_filter_unverifiable` when the
  hook has a filter and the ticket no longer exists, so the filter cannot be
  checked (see the filter, below).
  Changing a hook's URL clears its history, and deleting the hook deletes it.
  (`GET /admin/webhooks/{id}/deliveries`,
  `POST /admin/webhooks/{id}/deliveries/{delivery}/redeliver`.)
- **Webhook ticket filter** ([#413](https://github.com/PubliciaLLC/go-help-desk/issues/413)).
  A hook can carry one optional filter: sets of categories, priorities and
  assignee groups. A ticket matches a set when its value is any entry, and
  every non-empty set must match. `invert` sends for the tickets that do not
  match, which is how one hook takes "critical Network tickets" and another
  "everything else". No filter (the default, stored as `{}`) means every
  ticket, as before. A filter changes when a hook fires, never what it sends:
  the raw body is unchanged, and the classification travels on the event as
  fields excluded from JSON, the same way `Subject` and `StatusName` do. The
  filter is tested against the ticket's classification **as it was when the
  event was raised**, not when the outbox delivers, so a ticket reclassified
  in between is judged by its old values. Consequences worth knowing:
  auto-assignment happens inside the create's transaction, so a creation
  carries its group and a group condition matches a new ticket (the filter reads
  the event's `Ticket` state; see Ticket Events: One Change, One Event). Category covers the category's types and items. There are
  no type or item conditions yet. A filter naming a deleted group or category
  matches no ticket (inverted: every ticket). If a classification is not known
  (an event or a queued row made before this existed), the hook is sent to and
  a warning is logged: an extra message beats a missed one. A filtered-out
  event is not a delivery: it gets no per-hook outbox row, so no delivery id,
  no retry, no history entry and no result on the hook.

  **The hook's filter is read as it is now, at every step, not once.** It is
  tested when the event is fanned out to the hooks (above), again at **every
  attempt** of a queued delivery, retries included, and again at **Redeliver**.
  An administrator who tightens a filter therefore stops the deliveries
  already queued that it no longer admits: at their next attempt such a row
  is dropped and settled, with no send, no history entry and no failure, the
  same as a hook that was disabled, unsubscribed from the event or given a new
  URL. Widening a filter lets them go out. What is tested on a retry is the
  classification the row took when the event was raised: each per-hook row
  keeps it as internal data (ids and a priority, no text), which no body is
  built from. The raw body is still ids only, the chat and ITSM messages never
  show it, and neither the history copy nor the scrubbed row kept for 30 days
  after a final failure holds it. A row with no classification (queued before
  #413) is sent whatever the filter says, and a warning is logged.
  **Redeliver** has no stored classification to test (the kept copy has none,
  by design), so it reads it from the ticket as it is now, the way a chat
  message's content is rebuilt. If the filter does not admit that ticket it is
  refused with `409 webhook_filter_excludes` and nothing is queued. If the hook
  has a filter and the ticket cannot be read (deleted, or no source wired) the
  filter cannot be checked, and it is refused with
  `409 webhook_filter_unverifiable` and nothing is queued ([#499](https://github.com/PubliciaLLC/go-help-desk/issues/499)):
  Redeliver is something an administrator asked for just now, so it is told
  why rather than a message being sent that the filter might have stopped. An
  inverted filter is a filter; a hook with no conditions is not, and is
  replayed whether or not the ticket exists. A raw hook with no filter reads no
  ticket. Only a queued row with no stored classification (queued before
  #413, sent by the worker with nobody to tell) still fails open, with a
  warning logged. The replay's row carries
  the ticket's classification, so it is tested again at its own send. Refusal
  codes are in the API reference. (An earlier version of this section had the
  filter evaluated once, at the fan-out; that rule was reversed on #290.)
- **Delivery is queued, not done on the request**
  ([#164](https://github.com/PubliciaLLC/go-help-desk/issues/164)). A request
  that triggers a notification writes it to `notification_outbox`, one row per
  channel that can carry the event (`email`, `webhook`, and `verification`
  for signup and password-reset mail, below), and returns. For a change to a ticket the rows are
  written in the change's own transaction (Ticket Events: One Change, One
  Event). A worker in every server process
  claims due rows (`FOR UPDATE SKIP LOCKED`, so replicas never share one, with
  a ten-minute lease that returns a row whose worker died; a claim takes at
  most as many rows as can each run to their full time inside the
  lease — a minute for the database work, plus the 40 seconds the email
  channel's own SMTP limits allow — so rows are not reclaimed while still
  waiting their turn), sends, and deletes
  on success. A failed send is retried after 30 seconds, doubling to at most an
  hour, eight attempts in all; then the row is marked failed, logged, and
  deleted after thirty days. One row per channel means a failing channel is
  retried alone and the others are not sent twice; a channel that cannot carry an
  event type (webhooks never receive `guest.link_resent`) gets no row. A send
  that panics fails its row. A failed send's error is kept in `last_error` and
  the log with email addresses replaced by `[address]` (#350): a mail server's
  rejection usually names the recipient, and the SMTP status code is what an
  operator needs. The match is loose on purpose, so anything shaped like an
  address is hidden, including one with an apostrophe or an IP literal the
  relay never closed (#389). The reply is the relay's text, so the pattern is
  bounded to stay linear in its length (#358). It still costs about 0.4 s of
  worker CPU per megabyte of reply for the worst shapes on an Apple M4, and
  about three times that on a slower server, per attempt; that is accepted
  (#389). Delivery is at least once: a
  worker that dies between sending and settling sends again after the lease.
  **Webhooks are retried per hook** ([#448](https://github.com/PubliciaLLC/go-help-desk/issues/448)).
  The `webhook` row an event queues is a fan-out: the worker turns it into one
  row per subscribed hook and settles it. The row ids derive from the fan-out
  row, so a repeat after a crash reuses the same delivery ids: a row still
  waiting is not queued twice, and one already sent and deleted is queued again
  with the same `X-GHD-Delivery`, which a receiver can drop. A row holds no
  address; a raw hook's holds ids only, and a chat hook's keeps the text its
  message is built from until it is sent. When a chat hook's delivery finally
  fails (the eighth attempt) the worker keeps the failed row for the usual
  thirty days, but its event is first rewritten to ids only, so no subject or
  text lingers; a delivery that fails for good on the first try is deleted like
  any settled row. It holds a SHA-256 of the hook's URL,
  never the URL, which can carry a token. Each per-hook row
  is then delivered and retried on its own, on the schedule above: 30 seconds
  doubling to an hour, eight attempts in all, so a failing hook never causes
  another to be sent twice. The row's id is the `X-GHD-Delivery`. Connection
  errors, DNS and TLS failures, timeouts, 5xx and 429 are retried; a
  `Retry-After` from the receiver (seconds or a date) postpones the next attempt
  when it is longer than the schedule, to at most an hour. A refused address
  (`blocked_address`), any other status and a request that cannot be built are
  final: asking again gets the same answer. A hook disabled or deleted between
  queueing and sending is skipped, not failed, and so is one that no longer
  lists the event or whose URL no longer matches the stored hash (a queued
  retry for the old address is dropped rather than sent to the new one). This
  is checked on every attempt, so it also stops retries already waiting. The
  secret is read at send time, so a rotated secret signs the retry. A retry
  dropped for any of these reasons (hook disabled, no longer subscribed, URL
  changed, ticket filter no longer admits the ticket, hook paused because its
  API key, or that key's owner, is no longer active, body cannot be built)
  turns its History entry from "retrying" into **dropped**, with the reason
  (`result: dropped`, `drop_reason`: `disabled`, `unsubscribed`, `url_changed`,
  `filter`, `paused`, `unbuildable`; migration `000051`, #499). Only an entry that is
  retrying changes, and a drop is not an attempt. A deleted hook takes its
  history with it, so there is nothing to mark. Changing a hook's URL also
  clears its whole history (`UpdateWebhook`), so `url_changed` is seen only on
  an attempt that was in flight and recorded after the clear; in practice the
  admin sees an empty history, not a "Dropped" row, after a URL change. A refused address
  (`blocked_address`) is not a drop: it is a final result of its own attempt.
  Each attempt updates the hook's
  latest result and its history (above), not the outbox row, whose `last_error`
  holds only a class and a status. Delivery is at least once, so a receiver can
  see the same event twice.
- **Email is on only with both `SMTP_HOST` and `SMTP_FROM`**
  ([#456](https://github.com/PubliciaLLC/go-help-desk/issues/456)). The server
  says which at startup: `email enabled`, `email disabled: SMTP_HOST and
  SMTP_FROM are not set`, or a warning when exactly one of the two is set.
- **SMTP encryption** ([#455](https://github.com/PubliciaLLC/go-help-desk/issues/455)).
  There is no `SMTP_TLS` setting; the port and the credentials decide.
  - Port **465** uses TLS from the first byte (implicit TLS). Any other port
    connects in the clear and upgrades with STARTTLS when the server offers it.
  - **A password is never sent unencrypted**, to any address, private or not.
    With `SMTP_PASSWORD` set, TLS is required: implicit on 465, STARTTLS
    elsewhere, and if the server does not offer STARTTLS the send fails with
    `SMTP server does not offer STARTTLS, and SMTP_PASSWORD is set: refusing
    to send the password unencrypted`. Someone on the path stripping STARTTLS
    from the greeting therefore costs a failed send, not the password.
  - **With no password, plain SMTP is allowed only to a private address**:
    loopback, RFC 1918 (10/8, 172.16/12, 192.168/16) or IPv6 ULA (fc00::/7).
    STARTTLS is still used whenever it is offered. A public address with no
    password also requires TLS (`SMTP server does not offer STARTTLS and is not
    on a private address: refusing to send mail unencrypted`). Link-local and
    carrier-grade NAT ranges are not private for this rule. The check runs on
    the address the connection actually reached, not on the configured name.
  - **When `SMTP_USER` is set and the server offers no `AUTH`** (checked after
    STARTTLS, since AUTH is often advertised only then), the send fails with
    `SMTP server does not offer AUTH but SMTP_USER is set` instead of sending
    anonymously.
  - The certificate is always verified against the system roots. A user name
    with no password is not treated as a secret: only `SMTP_PASSWORD` triggers
    the requirement.
- **Email that cannot be sent is not silent.**
  - A template that fails to render is a *permanent* error. The worker marks
    the row failed at once, with no retries (a retry renders the same way), and
    keeps the error. It used to return success, so the row was deleted and the
    mail vanished.
  - `GET /admin/security-warnings` carries an `email` object: whether email is
    enabled and which of `SMTP_HOST` and `SMTP_FROM` is missing; whether signup
    or guest submission is on with email off; and whether email rows are
    failing (retrying after an error, or failed within the last day) with the
    newest address-redacted error. This is where an AUTH or STARTTLS failure
    shows.
  - **Turning signup or guest submission on while email is off is refused**
    (`400 email_required`), because both send an email the person needs. Only
    a change from off to on is refused, judged against the stored value, so
    saving the settings page with one already on is not. An install that
    already has either on with email off is warned, not switched off.
- **Signup verification email goes through the outbox too**
  ([#348](https://github.com/PubliciaLLC/go-help-desk/issues/348)), on its own
  `verification` channel. Signup answers the same 202 whether or not the
  address already has an account, and does the same work for both: it
  writes the pending registration and queues one row naming only that
  registration's id. The send re-reads the row and mails the stored
  address and token, or nothing if the row is gone or expired or the address
  has an account. Before this a new address dialled the mail server on the
  request and a taken one returned at once, so the timing said who had an
  account. A taken address now gets a pending row that is never mailed, and
  it expires unused. The send-time timing that remains is recorded under
  Guest Submission (#361).
- **The display name and password are chosen at verification, not at
  signup** ([#360](https://github.com/PubliciaLLC/go-help-desk/issues/360),
  [#374](https://github.com/PubliciaLLC/go-help-desk/issues/374)). The
  signup form takes only the address. The link in the verification email
  opens a page that asks for the display name and the password, neither
  pre-filled, and `POST /auth/verify-email` takes the token, `display_name`
  and `password` together. A blank name or a password below the usual
  minimum is refused without using up the link. When signup carried them, a
  second signup for the same address replaced them, so anyone who knew an
  address could sign up after its owner and have the owner, following the
  newest link, create an account with the other person's password, and
  later with their name. A second signup can now only re-issue the link.
  This holds because only the inbox's owner reaches the page (the email is
  never sent for an address that already has an account), and signup still
  does the same work for a new and a taken address (#348).
  `pending_registrations.password_hash` and `display_name` are no longer
  written and are dropped in a later migration.
  The page first looks the link up (`GET /auth/verify-email?token=`, which
  answers the address and nothing else, and refuses a dead link the same way
  the POST does), so it shows which address the account is for, gives a
  password manager the address, and says a dead link is dead before
  anything is typed
  ([#370](https://github.com/PubliciaLLC/go-help-desk/issues/370)). When MFA
  is required for requesters, the verified session owes enrolment like a
  password login does, and the page goes to the same enrolment form before
  the dashboard ([#369](https://github.com/PubliciaLLC/go-help-desk/issues/369)).
- **Chat/ITSM payload formats (Slack, Teams, Discord, JIRA)** — shipped in v1.
  Not a plugin, and not a separate integration surface: a webhook
  subscription has a `payload_format` setting (`raw`, the default,
  `slack`, `teams`, `discord`, `jira`) that reshapes the same lifecycle event
  into the body that service expects (Slack/Discord: a message body such as
  `text`/`content` plus blocks or an embed; Teams: an Adaptive Card; JIRA: a
  comment/webhook-automation-compatible body) before it is POSTed to the
  operator's configured URL — a Slack incoming webhook, a Teams connector URL,
  a Discord webhook URL, or a JIRA automation webhook endpoint. This reuses the
  existing webhook admin surface, its SSRF address-checking, and its
  administrator-only registration; it adds a format field and a set of
  renderers, not a new credential type, a new event source, or new
  infrastructure. Detailed field-by-field mapping per service is tracked in
  [#187](https://github.com/PubliciaLLC/go-help-desk/issues/187) rather than
  duplicated here.
- Anything beyond reshaping this payload — a plugin polling Slack for replies,
  a two-way JIRA sync, a Discord bot — is genuinely plugin territory and stays
  on the v2 plugin timeline above.

---

## Ticket Events: One Change, One Event

Built in [#506](https://github.com/PubliciaLLC/go-help-desk/issues/506) (merged
as #510, 1.3.0). A change to a ticket is **one event for that ticket**, written
to the notification outbox in the same database transaction as the change.
Every reader of that event (webhooks, requester email, staff email) decides
from it what it is told. Code paths below are relative to `backend/` and were
checked against the code at `cbe98585`. Where this text and the code ever
disagree, the code wins and this text is wrong.

**The goal, in Erik's words (2026-10-14):** "a single event carrying the
entire change needs to fire off, be put on the event bus for the web hooks to
receive and process, and every Web hook that meets a criteria for that change
event on the work order needs to fire its payload."

### How it works

#### The bus

The bus is the `notification_outbox` table, the `notification.Dispatcher`
interface and the outbox worker. There is no external message broker.

**The outbox row is written inside the change's transaction.** An early
version of the #506 issue text said so, but at that time the code wrote the
rows after the commit, so a crash in between lost the notification. That gap
is closed. `txrunner.Runner.InTx` (`internal/database/txrunner/txrunner.go`)
hands the function a collector (`*notification.Changes`) as its third
argument. Just before the commit, it writes one outbox row per channel for each
event the function raised (`EnqueueNotification`, on the transaction's own
connection). A rolled-back change leaves no row. A committed change cannot
lose its announcement. After the commit it wakes the worker.

What happens to a change, step by step:

1. **The producer** (a ticket service method) does its writes inside
   `ticket.Atomic.InTx` and *raises* what it did into the collector. The
   collector merges everything raised for one ticket into one
   `notification.Event` (`internal/domain/notification/changes.go`).
2. **The transaction writes the rows.** For each merged event, one row per
   channel that can carry it: `email` (when there is a requester recipient,
   a guest link, a staff user, or the change may mean staff mail), `webhook`
   (when any of its kinds is a webhook event name) and `verification` (signup
   verification mail and password-reset mail, never any other event)
   (`notification.Carries`, `channels.go`). The channel list is
   `email`, `webhook`, `verification` (`cmd/server/outbox_wiring.go`).
   When Postgres aborts the transaction for a deadlock (`40P01`) or a
   serialization failure (`40001`), `InTx` runs the function again with a new
   collector, so the rolled-back attempt's events are discarded with its
   writes. Three attempts in all, with a 10-50 ms pause between them, and
   none after the request's context ends (`internal/database/dbretry`, #508).
   The same rule covers `outboxstore.Split` and the user store's guarded
   writes. A function given to `InTx` must start from a clean slate on every
   run: nothing it captured may carry over from an attempt that was rolled
   back. **Not covered, on purpose:** the `unlock` command, which is run by
   hand and shows its error to the person running it, and a single
   autocommit statement, which is not wrapped and returns the error if it is
   aborted (the three guarded last-administrator statements are the one
   exception, because the deadlock was measured there).
3. **The worker** runs in every server process. Each turn it claims rows that
   are due (`FOR UPDATE SKIP LOCKED`, ten-minute lease) and hands each to the
   sender registered for the row's channel (`internal/server/notify/outbox.go`).
4. **The senders.** The `email` channel is `ChangeSplitter`, wrapped around
   `StaffMailDispatcher`, wrapped around `GuestLinkDispatcher`, wrapped around
   `EmailDispatcher` (`cmd/server/main.go`). `webhook` is `WebhookDispatcher`.
   `verification` is `VerificationDispatcher`.
   - `ChangeSplitter` (`notify/split.go`) works out who is mailed and turns the
     change's one email row into one row for the requester's mail and one row
     for each staff member (see Staff email). Rows that carry no `Kinds` are
     sent as they are.
   - `WebhookDispatcher` (`notify/webhook.go`) turns the change's webhook row
     into one row for each hook that matches (the per-hook rows of #448). A row
     that names a hook is one delivery.
   - `StaffMailDispatcher` reads the staff member's address and runs
     `StillEntitled` at send time. `GuestLinkDispatcher` issues the guest token
     at send time.
5. **Settling.** A successful send deletes the row. A failure is retried after
   30 seconds, doubling to at most an hour, eight attempts in all. A permanent
   error, or the eighth failure, marks the row failed. Failed rows are kept for
   30 days.

**How does a reader know it has not already handled a row?** The readers do not
look. Nothing polls the table except the worker. Each row names one channel,
and the worker calls that channel's sender with that row. A row is handled when
it is deleted, and deletion happens only after a successful send. Two workers
never hold the same row: the claim skips locked rows and sets a lease. If a
worker dies after sending but before deleting, the lease runs out and the row is
sent again. Delivery is therefore "at least once". For webhooks,
`X-GHD-Delivery` is the same on every retry, so the receiver can drop a
duplicate. For email there is no such id, and a duplicate mail is accepted.

**Not every event goes through the transaction.** Two events are not changes to
a ticket and have no transaction to join: `guest.link_resent` (a guest asking
for a new link) and `registration.verify` (signup mail). They are still
dispatched after their own write through `notify.OutboxDispatcher`, on a
context that cannot be cancelled. Neither is a webhook event.

**Plugins.** Nothing reads the bus as a plugin. A plugin reader would be one
more channel name in `outboxChannels` and one more sender in the worker's map;
no plugin API is built.

#### Producers: what each door raises

All in `internal/domain/ticket/service.go` unless named otherwise. Every row
raises into the transaction's collector; none dispatches after the commit.
"Status kinds" is defined under *The event* below.

| Producer (who calls it) | Kinds it raises | What the event carries |
| --- | --- | --- |
| `create` (REST and guest `POST` ticket, MCP `create_ticket`, `CreateFollowUp`, `CreateRequesterFollowUp`) | `ticket.created`; plus `ticket.assigned` when routing picked someone (REST create, REST follow-up and guest follow-up only; guest create, MCP `create_ticket` and MCP `create_follow_up` pass no candidates) | Recipient = guest address and the guest-link flag for a guest. For routing, the `RoutedAssignment` flag (below). |
| same, follow-up only | `ticket.linked` **on the original ticket** (its own event: one per ticket) | `target_id` = the new ticket, `link_type` = `parent_child` |
| `UpdateStatus` (MCP `update_ticket_status`; REST `PATCH status_id` runs the same in-transaction step inside `Update`) | the status kinds | `new_status_id`; recipient = guest (none when closing); guest-link flag unless closing |
| `Assign` (MCP `assign_ticket`; REST `PATCH` assignee or `clear_assignee` runs the same step inside `Update`) | `ticket.assigned`, only when the assignee user or group moved. A clear of an assigned ticket is a change; a clear of an unassigned one, or assigning the person and group already held, raises nothing (ruling 16). The audit entry is still written, as for a same-status move. | |
| `ChangePriority` (no production caller: REST `PATCH priority` runs its step inside `Update`) | `ticket.priority_changed`, only when the priority moved | |
| `UpdateCTI` (no production caller: REST `PATCH category_id` runs its step inside `Update`) | `ticket.classified`, only when category, type or item changed. Now in a transaction; still writes no audit entry. | |
| `Update` (REST `PATCH /tickets/{id}`, `handler_tickets.go`; it calls the in-transaction step functions of the four rows above) | whatever its steps raise, merged | see *One transaction* |
| `addReply` (REST reply, MCP `add_reply`, guest reply) | `ticket.replied`, or `ticket.note_added` for an internal note. A requester's reply that reopens a Resolved ticket adds the status kinds (`status_changed`, `reopened`) and, on the ticket's first reopen, `priority_changed` if the priority moved (a critical ticket cannot be raised). | `reply_id`, `internal`, `ReplyBody` (public only); recipient = reporter when "notify customer" is on; `FromRequester` true for the requester's public reply **including one that reopens** |
| `Resolve` (REST resolve) | the status kinds into Resolved (`status_changed`, `resolved`). On an already-Resolved ticket with new notes: `ticket.resolved` only. | `new_status_id`; recipient = guest; guest-link flag |
| `ResolveAsDuplicate` (REST link with `duplicate_of` and resolve) | `ticket.linked` (only when it made the link) merged with the resolve above | `target_id`, `link_type`, then as `Resolve` |
| `close` (REST close, auto-close scheduler as the system) | the status kinds into Closed (`status_changed`, `closed`) | no recipient |
| `Reopen` (REST `POST /reopen`) | the status kinds (`status_changed`, `reopened`), plus `priority_changed` on the first reopen, if the priority moved | recipient = guest; guest-link flag |
| `AddLink` (REST link) | `ticket.linked` on the source ticket | `target_id`, `link_type` |
| `RequestGuestLink` (guest "resend my link") | `guest.link_resent` through `OutboxDispatcher` | what the guest typed; no ticket id |
| `registration.Service.Signup` | `registration.verify` through `OutboxDispatcher` | `pending_id` only |

Writes that change a ticket and announce nothing: `RemoveLink`, attachments,
follow and unfollow, and the bulk unassignments `UnassignForUser` and
`DeleteGroup`. (The last two move tickets without telling the new holder.
That is a gap, but a separate one.) The `staff.*` events are not produced by
domain code at all: the worker makes them (Staff email).

#### Consumers

| Consumer | Reads | Decides by |
| --- | --- | --- |
| Requester email (`EmailDispatcher`, `notify/email.go`) | the requester's `email` row, made by `ChangeSplitter` | `Type` → template: created, reopened, resolved, replied, status_changed, guest_link. Needs a `Recipient` or a guest link. Status, resolve and reopen mail go to guests only. |
| Staff email (`ChangeSplitter`, then `StaffMailDispatcher` + `StillEntitled` at send) | the change's `email` row, then the `staff.*` rows | `Kinds` → candidates (assigned, requester replied, reopened). Then switches, overrides, actor skip, one mail per person. At send: account still mailable, still holds, still in group, or still follows and can see the ticket. |
| Guest link (`GuestLinkDispatcher`) | `email` row with the guest-link flag | issues and rotates the token at send time |
| Verification (`VerificationDispatcher`) | `verification` row | `Type`: `registration.verify` is sent by `pending_id` (the address and token are read from the pending row); `password.reset_requested` carries the typed address as its recipient and no `pending_id`, and is handed to the password-reset send step, which looks the account up and makes the token at send time (#451) |
| Webhooks (`WebhookDispatcher`) | `webhook` row | `subscribedKinds(hook, event)`: any kind of the change in the hook's list, or `*`, or an empty list. Then the ticket filter on `Ticket`. Again at every retry and at Redeliver. |
| Chat and ITSM formats (`notify/format*.go`) | the per-hook row | headline chosen by `Type`; "Also" line, reply text and status name chosen by the kinds the hook subscribes to |
| Plugin registry | nothing | never called |

#### What subscribers are promised

The **raw body** is ids only: `Type`, `TicketID`, `ActorID`, `OccurredAt`, and
a `Payload` holding only the id that belongs to the name the body is sent
under: `new_status_id` for `ticket.status_changed`, `target_id` for
`ticket.linked`, nothing for any other name (`rawKeysFor`,
`notify/webhook_body.go`). Headers: `X-GHD-Delivery` and `X-GHD-Event` (the same
name as `Type`), plus the signature. The OpenAPI `WebhookEvent` enum lists `*`
and the eleven names. The raw body already changed during the 1.3 betas
(#448), and `docs/upgrade-notes-1.3.0.md` describes both changes to
1.2.x users.

#### One action, what a `*` hook receives

| One user action | Events | Deliveries to a `*` hook |
| --- | --- | --- |
| Staff create (REST), auto-assigned | one event: `created`, `assigned` | 1 |
| Follow-up, auto-assigned | new ticket: `created`, `assigned`; original: `linked` | 2, over two tickets |
| `PATCH` status + assignee + priority | one event: the status kinds, `assigned`, `priority_changed` | 1 |
| `PATCH` out of Resolved/Closed | one event: `reopened`, `status_changed` (+ `priority_changed` on the first reopen, if the priority moved) | 1 |
| Requester reply that reopens | one event: `reopened`, `status_changed`, `replied` (+ `priority_changed` on the first reopen, if the priority moved) | 1 |
| `POST /reopen` | one event: `reopened`, `status_changed` (+ `priority_changed` on the first reopen, if the priority moved) | 1 |
| Resolve as duplicate | one event: `resolved`, `status_changed`, `linked` | 1 |
| `PATCH` status to Resolved | `resolved`, `status_changed` | 1 |
| Resolve button | `resolved`, `status_changed` | 1 |
| Close (button, scheduler, or `PATCH` to Closed) | `closed`, `status_changed` | 1 |
| `PATCH` or assign that repeats the current assignee and group, or clears an unassigned ticket | nothing (the audit entry is still written) | 0 |
| `PATCH` that repeats the ticket's current status | nothing about status (see *Same status*) | 0, or 1 for the other kinds in the request |
| Resolve button on an already-Resolved ticket, new notes | `resolved` only | 1 |
| `PATCH` priority only | `priority_changed` | 1 |
| `PATCH` category only | `classified` | 1 |

The same status change has the same names whichever button made it. A hook
subscribed to `ticket.resolved` sees a resolve done with `PATCH`, and a hook
subscribed to `ticket.status_changed` sees a resolve done with the button and
every close.

### The event

#### 1. One event per ticket per committed transaction

One transaction that changes a ticket produces **one event for that ticket**.
The event lists everything that changed and the ticket's state after the
commit. A transaction that changes two tickets produces one event for each (a
follow-up: the new ticket is created, and the original is linked). "On the work
order" in Erik's sentence is the ticket, so a hook fires at most once per ticket
per change.

`notification.Event` (`internal/domain/notification/notification.go`) keeps its
older fields and has three more, none of which is sent in a raw body:

- **`Kinds`**: what happened, as the event names, in a fixed order (below).
  Consumers match on this.
- **`Changes`**: field by field, from and to. The fields raised are
  `status_id`, `assignee_user_id`, `assignee_group_id`, `priority`,
  `category_id`, `type_id` and `item_id`. Ids and enums only, never text.
- **`Ticket`** (`TicketState`): the ticket as committed: those same seven
  fields plus `reopen_count`. It replaces the old `Attrs`, `AssigneeUserID`
  and `AssigneeGroupID`, which were each a part of it. The webhook filter and
  staff mail read it.

`Type` stays on the event and is set to the first entry of `Kinds`. The fixed
order (`notification.KindOrder`) is the order of importance:
`ticket.created`, `ticket.reopened`, `ticket.resolved`, `ticket.closed`,
`ticket.status_changed`, `ticket.assigned`, `ticket.priority_changed`,
`ticket.classified`, `ticket.replied`, `ticket.note_added`, `ticket.linked`.
Two kinds are new: `ticket.priority_changed` and `ticket.classified`
(category, type or item). Both can be subscribed to.

**Status kinds are the same for every door** (`statusKinds`,
`internal/domain/ticket/events.go`). Every status move carries
`ticket.status_changed`. A move into Resolved also carries `ticket.resolved`. A
move into Closed also carries `ticket.closed`. A move out of Resolved or Closed
into an open status also carries `ticket.reopened`. This is the same for the
buttons, `PATCH`, MCP, a requester's reply and the scheduler.

**Same status.** A move to the status the ticket already has is not a move: it
carries no status kind and no `status_id` change (Erik's ruling 13, below).
Resolving an already-Resolved ticket with the Resolve button and new notes is
the one exception, because that is a resolution; it carries `ticket.resolved`
only (`resolveInTx`). Any other door repeating Resolved announces nothing about
status, so a `PATCH` that repeats Resolved and assigns someone sends
`ticket.assigned` only, and the guest is not mailed or given a new link. An
event that ends up with no kinds at all is ignored by the collector and writes
no row.

Example: an agent `PATCH`es a Resolved ticket back to In Progress and assigns it
to Bob, and this is the ticket's first reopen. The event, as the server holds
it:

```json
{
  "Type": "ticket.reopened",
  "TicketID": "7d0c…",
  "ActorID": "a11c…",
  "OccurredAt": "2026-10-14T15:02:11Z",
  "Kinds": ["ticket.reopened", "ticket.status_changed",
            "ticket.assigned", "ticket.priority_changed"],
  "Changes": [
    {"field": "status_id",        "from": "<resolved>", "to": "<in progress>"},
    {"field": "priority",         "from": "medium",     "to": "high"},
    {"field": "assignee_user_id", "from": "<alice>",    "to": "<bob>"}
  ],
  "Ticket": {
    "status_id": "<in progress>", "priority": "high",
    "assignee_user_id": "<bob>", "assignee_group_id": "<network>",
    "category_id": "<hardware>", "type_id": null, "item_id": null,
    "reopen_count": 1
  },
  "Payload": {"new_status_id": "<in progress>"}
}
```

(Shown as the server holds it; `Payload` holds the keys of every kind and is cut
down for the wire, below. Inside the server the other fields are also kept:
recipient, guest-link flag, tracking number, subject and status name. They are
never sent to a raw webhook.)

**How the collector merges** (`Changes.Raise`): kinds are a set, kept in the
fixed order; changes are keyed by field, with the first "from" and the last
"to"; `Ticket` is the last one raised, so the final raise must describe the
ticket as the transaction leaves it; `Payload` keys are merged, a later key
replacing an earlier one; the strings (tracking number, recipient, subject,
status name) are the last non-empty one; the flags (guest link, `FromRequester`,
`RoutedAssignment`) are true if any piece said so; the actor is the first
non-nil one raised and the time is the first piece's. One event has one
actor; a routed create's actor is the creator's, and the webhook sender drops it
for the name `ticket.assigned` (see *Create, then auto-assign*).

#### 2. One transaction

A change is one transaction. Each service method raises its kinds and changes
inside its `InTx`, into the collector, instead of calling `Dispatch` after the
commit. A door that does several things calls the pieces inside one
transaction, and the collector merges them.

- **`PATCH /tickets/{id}` is one service method**,
  `Update(ctx, id, Patch, actor)`. It runs the existing steps in the existing
  order (status, assignment, classification, priority) inside one `InTx`.
  The `Validate*` checks the handler makes up front stay as the early answer
  (`handler_tickets.go`). Because it is one transaction, `PATCH` is
  all-or-nothing: a refusal partway through leaves the whole request unapplied.
  The priority is last so that a category changed in the same request is the
  one its SLA policy is matched against.
- **Create, then auto-assign.** Routing is inside the create's transaction. The
  handler still works out the candidates before the transaction (`routing` in
  `handler_tickets.go`: the CTI group, else the global group, else the
  round-robin users) and passes them in `CreateInput.AutoAssign`. Inside the
  transaction, the first candidate that `checkAssignable` accepts is assigned; a
  candidate refused as unassignable (a departed colleague still on the
  round-robin list) is skipped for the next. The event is `[created, assigned]`.
  A group filter therefore matches a new ticket. Routing also writes its own
  audit entry `assigned`, with no actor and the change `unassigned` to
  `assigned`. The event carries the creator as its one actor, but routing is the
  system's act: a hook that is told `ticket.assigned` gets `ActorID: null`
  (`forHook`, `notify/webhook.go`, from `RoutedAssignment`), and one told
  `ticket.created` still gets the creator (ruling 17). Limit: only create's routing
  sets the flag and create raises no manual assignment in the same transaction; if
  a future change lets one transaction raise both, the merge would null the
  human's actor under `ticket.assigned`, and per-kind actors would be needed. Guest create, MCP `create_ticket` and MCP `create_follow_up` do not auto-assign, as before.
  - **Round-robin takes its turn up front.** When no group routes the ticket,
    `roundRobin` advances its counter with one atomic add as `routing` runs,
    before the transaction, and offers the whole user list rotated from that
    slot. A create that is then refused still used a turn, and two overlapping
    creates each get their own.
  - **A creator who is routed their own ticket is mailed**: routing picked the
    assignee, so the event carries `RoutedAssignment`, which exempts it from "no
    mail about your own action" (Staff email). The same flag makes the
    `ticket.assigned` webhook name no actor.

#### 3. The outbox write is inside the change's transaction

This is a true transactional outbox. The `Atomic` contract
(`ticket/store.go`) is `InTx(ctx, fn func(Store, audit.Store,
*notification.Changes) error)`. A separate "announcing" `InTx` was rejected: a
producer would have had two ways in, and one of them would not announce. The
recorded decision "`ticket.Atomic` takes both a `Store` and an `audit.Store`"
extends to the change's events for the same reason: an announcement made apart
from the change it describes is not reliable.

- **The row encoding** (`EncodeEvent` and `DecodeEvent`) lives in the
  `notification` package (`codec.go`), so `txrunner` can write rows without
  importing the server. It refuses an event carrying a raw guest token.
- **The detached context** is no longer needed on the ticket path. If the client
  disconnects mid-transaction, the change and its events roll back together.
  `OutboxDispatcher` keeps its detached context for the two producers that
  still dispatch after their own write.
- **No schema change and no migration.** `notification_outbox.event` and
  `webhook_deliveries.event` are `jsonb`, and the new fields are new keys.

**Staff fan-out moved to the worker.** The fan-out reads group members and
followers through ordinary connections. Inside the change's transaction those
reads would not see the transaction's own writes. For example, `Assign` drops
the previous assignee's follow in the same transaction, so a read inside it
would still see that follow and mail the previous assignee as a follower. So
who is mailed is decided when the worker first takes the change's email row
(normally within a second of the commit), not on the request.

**The split is all or nothing.** `ChangeSplitter` collects the requester's mail
and each staff member's mail, then `outboxstore.Store.Split`
(`internal/database/outboxstore/outboxstore.go`) queues them all and deletes
the change's row in **one transaction**. A failure part way leaves nothing
queued and the change's row to be tried again. The transaction deletes the
change's row **first**, and only the call that deleted it queues anything
(#513): a split that outlasts the 300-second claim lease lets a second worker
claim and split the same row, and the first split's children may be sent and
deleted by then, so `ON CONFLICT` alone would queue them again and the mail go
out twice. The second worker finds no row to delete, queues nothing and
succeeds. The children's ids derive from
the change's row id (`uuid.NewSHA1` of the parent and a key: `requester`, or
`staff:` plus the user id), so a repeat after a crash does not queue anyone twice
while their rows still wait. Outside the worker (no row id) it queues one by
one. `Split` on a store built without a database handle (`outboxstore.New`)
returns an error rather than queue and delete as separate commits, which would
lose mail when an insert failed after the parent was gone; production builds
the store with `NewWithDB`. A failed read of the staff
accounts, of the followers or of a group's members returns an error and the row
is retried with nothing queued (ruling 15). This is deliberate: a requester's
mail queued before that error would by then have been sent and deleted, and sent
a second time, with a new guest link each time, and mailing the people found so
far would lose the rest with no retry. The cost is that a lookup that keeps
failing holds back every mail of the change, the requester's included: it is
tried 8 times in all over about an hour, then the row is marked failed. The log says
"staff email: could not work out who to notify".

#### 4. A consumer is still a channel row (no shared log with positions)

One row per channel is kept, written with the change. A shared event log that
each consumer reads from its own position was considered and not built.

- Each channel needs its own retry state and "done" marker. Today that is the
  row: its attempts, its next try, and its deletion. Webhooks need this per hook
  as well (#448). A shared log would still need a table of per-consumer,
  per-event state for retries, which is the outbox again plus a position.
- A position in Postgres is unsafe without extra machinery. A sequence number is
  given out when a row is inserted, not when it commits, so a reader that has
  read up to 105 can miss 104 if 104 commits after 105.

#### 5. How each consumer matches

**Webhooks.** A hook matches a change if any entry in `Kinds` is in the hook's
event list, or if the list is `*` or empty (`subscribedKinds`,
`notify/webhook.go`; for `*` only names that can be webhook events count). The
ticket filter is then tested against `Ticket`, as before. A matching hook gets
**one** per-hook row, so one delivery. Retries run the same test against the
subscription as it is at that moment, and the filter as it is at that moment.
The rules for a changed URL hash, a disabled hook and a deleted hook are
unchanged.

**What the hook is told the event "is" (`Type` and `X-GHD-Event`).** It is the
first kind in `Kinds`, in the fixed order, that **this hook** subscribes to
(`forHook`). For `*` it is simply the first kind. It is worked out again at each
attempt, from the subscription at that moment. So a hook subscribed only to
`ticket.status_changed` gets `"Type": "ticket.status_changed"` for the reopen
example above. The history's `event_type` column stores the `Type` that was
sent.

**Raw body (Erik, 2026-10-15): the shape is unchanged.** `Kinds`, `Changes` and
`Ticket` live on the event inside the server, so that matching and the filter
can use them. They are not sent. The raw body is `Type`, `TicketID`, `ActorID`,
`OccurredAt` and the allow-listed `Payload` id, and the id goes out only for the
name it belongs to (see *What subscribers are promised*). So a `*` hook that is
told `ticket.reopened` for the example above receives no `Payload`, where the
server's own copy holds `new_status_id`; a `ticket.priority_changed` or
`ticket.classified` delivery has none either, and the receiver reads the ticket
through the API by `TicketID`. The tracking number stays out, as #448 ruled on
2026-10-13. There is no version flag.

**Chat and ITSM formats** (Slack, Teams, Discord, Jira Automation). The
headline comes from the hook's `Type`, as before. The headline for
`ticket.priority_changed` is "Priority changed" and for `ticket.classified`
"Reclassified" (`headline`, `notify/format.go`); neither carries a value or a
name. Jira has no headline and keeps the event name in its `event` key. Under
the headline:

- **The "Also" line (ruling 10).** One line names the change's other kinds, such
  as "Also: assigned, priority changed". It names **only kinds the hook
  subscribes to**. A `*` hook sees every other kind. A hook subscribed to a
  single name gets no Also line, and Jira gets no `also` key. The line never
  tells a channel about something it did not subscribe to: an `assigned`-only
  Slack hook must not learn that an internal note was added. It names kinds
  only, never values, because a status or group *name* would be content. The
  words are: created, reopened, resolved, closed, status changed, assigned,
  priority changed, reclassified, replied, note added, linked. It is rebuilt at
  every attempt, so a retry after the subscription narrowed drops the kind. A
  Redeliver has no kinds beyond the one name, so it has no Also line.
- **Reply text (ruling 12).** The text of a reply goes only to a hook that
  subscribes to `ticket.replied` (or `*`). A hook subscribed to
  `ticket.reopened` only still gets its "Reopened" message, with the subject and
  the link, but not the text of the reply that reopened the ticket. Internal
  notes are never included, whatever the hook subscribes to. Jira follows the
  same rule as chat.
- **Status name.** The status name is carried only for a hook that subscribes to
  a status kind (`ticket.status_changed`, `ticket.resolved`, `ticket.closed` or
  `ticket.reopened`). In Jira that is the `status` key; a hook on
  `ticket.assigned` alone does not learn the status of a change that also moved
  it.

**Requester email.** One mail per change at most. The recipient is decided once
per change, when the change is raised:

- the reporter for a reply with "notify customer" on;
- the guest for a creation, a status change other than closing, a resolution,
  and a force-reopen;
- nobody for a close.

The template is the first kind in the fixed order that has a requester template:
created, reopened, resolved, replied, status_changed (`requesterTemplates`,
`notify/staff.go`). Some guest mail changed as a result. A `PATCH` to Resolved
now says "We have resolved" instead of "There is an update". A status change
that reopens now says "We have reopened". The guest-link flag is set once per
change, so a link rotates once per change.

**Staff email** (`ChangeSplitter` and `StaffFanout`, `notify/split.go` and
`notify/staff.go`). Candidates are worked out for each kind with the existing
rules (assigned, requester replied, reopened), all from `Ticket`, the state
after the commit. They are merged with one entry per person. A person who
qualifies for more than one reason gets the mail of the **first reason, in the
order assigned, reopened, requester replied, whose switch is on**. Every rule in
Notifications still holds:

- one mail per person per change;
- nobody is mailed about their own action, even when an override forces the
  switch on (checked after the switches), **except** an assignment that routing
  made inside a create (`RoutedAssignment`): the creator did not choose it, and
  1.2.x mailed them, so they still get that one mail (ruling 11);
- only active staff with an address; never a service account or a requester;
- the three switches and the administrator's overrides;
- the follower exclusions (the assignee never gets follower mail; group members
  are excluded from follower mail when the group is mailed for that kind);
- the send-time re-check (account, holding, group, follow and scope).

`FromRequester` stays, so "requester replied" means the filer's public reply. It
is **true on a reply that also reopened the ticket**. The old special case "not
when the reply reopened" is gone: a reply that reopens is one change carrying
both kinds, and the merge gives each person one mail.

**One mail per address (ruling 11).** After the staff are worked out, the mails
are deduplicated by recipient address, compared without case or surrounding
space. Erik's ruling is only that one address gets one mail; which mail is kept is a build decision. The stronger reason is kept:

- If the requester's address is also a staff member's (a staff member who filed
  the ticket for themselves, or whose address a guest used), the **staff mail is
  kept** and the requester's mail is dropped. The staff mail carries the more
  specific reason and the link behind sign-in; the requester's mail would add a
  guest link, a second credential in the same inbox.
- If two staff accounts share an address, the first in candidate order is kept,
  which is strongest reason first: holding the ticket, then its group, then
  following it.
- Two different addresses keep both mails. A requester mail whose address is
  only read at send time (a guest-link mail) cannot be compared and is sent.
- The send-time check can still drop a kept staff mail if the person stops being
  entitled between the split and the send, because rows know nothing of each
  other. That is accepted.

Example, the reopen plus reassignment above: Bob, the new assignee, gets
"assigned to you" (his Assigned switch). If that switch is off, he gets
"reopened" (his Activity switch). A follower of the ticket who is neither Bob
nor in the group gets **one** mail: the follower version of "assigned" ("A ticket
you follow is assigned"), because `assigned` comes first in the order and the
follower qualifies for it under the Activity switch; they are not also sent the
"reopened" mail. Alice, the previous assignee, gets nothing: the holder is read
from `Ticket`, after the commit.

#### 6. Compatibility for existing webhook subscribers

- **Today's subscriptions keep working as criteria.** The event list on a hook
  is unchanged, and `Kinds` uses the same names. No stored hook changes, and no
  migration is needed.
- **A hook subscribed to `ticket.status_changed`, for a change that also
  reopened the ticket,** receives one delivery with `Type: ticket.status_changed`
  and the same `Payload.new_status_id` as before. A hook subscribed to a single
  name gets the body it always had.
- **What does change for receivers** (these are in the upgrade notes):
  - A `*` hook, or a hook with several names, gets one delivery where it got two
    or three. A receiver that switches on `Type` and handles only, say,
    `ticket.replied` will not see the reply in a reply that reopened a ticket,
    because `Type` is `ticket.reopened` and the body does not list the other
    kinds. **Accepted (Erik, 2026-10-15, ruling 6).** The upgrade notes tell such
    receivers to read the ticket through the API when they need more than the
    label.
  - Hooks fire where they did not before, because the status kinds are now
    consistent: `ticket.status_changed` on the Resolve button, on a close and on a
    requester's reopening reply, and `ticket.resolved` and `ticket.closed` on a
    `PATCH`.
  - `*` hooks start receiving `ticket.priority_changed` and `ticket.classified`.
  - A status change to the status the ticket already has no longer announces
    `ticket.status_changed` (1.2.x did).
- **Redeliver.** It sends the name that was sent, because the kept copy holds no
  `Kinds`. `409 webhook_not_subscribed` therefore means the hook no longer
  subscribes to the `Type` of that delivery (not "to none of the change's
  kinds", which is not recorded). `webhook_filter_excludes` and
  `webhook_filter_unverifiable` are unchanged.
- **Rows queued in the old shape during the upgrade.** Outbox rows and webhook
  history copies are JSON. A row made before #506 has no `kinds`. `DecodeEvent`
  leaves `Kinds` **empty** and fills `Ticket` from the old `attrs`,
  `assignee_user_id` and `assignee_group_id` keys; every consumer reads an event
  with no `Kinds` as a one-kind change, `[Type]` (`Event.AllKinds`). An old
  webhook row, per-hook or not, is therefore delivered as before, and an old
  history entry redelivers as before. An old `email` row had already been split
  per person on the request, so it carries no `Kinds`, and `ChangeSplitter` sends
  it as it is and does not split it again. The same rule keeps the worker from
  splitting its own children: the rows it makes carry no `Kinds`. Tested with
  stored rows in the old shape (`codec_test.go`, `webhook_one_delivery_test.go`,
  `split_test.go`).

#### 7. Events that are not ticket changes

`guest.link_resent` and `registration.verify` are not changes to a ticket. Each
is already a single event for a single action. Neither is a webhook event. They
keep their path (dispatch after their own write, on a context that cannot be
cancelled). Registration could be made transactional the same way later; nothing
in #506 asks for it. The `staff.*` events remain internal rows made by the
worker's split. They are never dispatched by domain code and never sent to
webhooks.

#### 8. Out of scope (not built)

- Ordering between events. Two changes to one ticket can still arrive out of
  order after a retry, as before.
- Events for writes that announce nothing: `RemoveLink`, attachments, follow and
  unfollow, and the bulk `UnassignForUser` and `DeleteGroup`.
- An audit entry for a classification change (`UpdateCTI` still writes none).
- Auto-assignment for guest-created and MCP-created tickets.
- A plugin API.
- `reply_id` on the raw wire. A receiver still cannot tell which reply a
  `ticket.replied` delivery means without listing replies.
- A message broker.

#### 9. Tests

Test-first, as CLAUDE.md requires. The outbox and transaction tests use real
Postgres, and the ones that commit to the outbox run on a database of their own
(`testutil.NewIsolatedDB`) so no other package's claims or cleanups can touch
their rows.

- **The collector** (pure): `domain/notification/changes_test.go`. Kinds merged
  in the fixed order, first-from and last-to, one event per ticket, an empty
  collector or an event with no kinds writes nothing.
- **The codec**: `domain/notification/codec_test.go`. Round trip, rows in the old
  shape, and a test that fails when a field is added to `Event` without a
  decision about how it is stored.
- **Producers**: `domain/ticket/one_event_test.go`. One event per change and per
  ticket, routing inside the create's transaction, a failing `PATCH` step
  announcing nothing, the same-status rules.
- **Transactional outbox** (Postgres): `database/outboxstore/txrunner_outbox_test.go`.
  A rolled-back change leaves no row, a committed one leaves one row per
  carrying channel, a failed outbox write rolls the change back, a deadlock retry
  announces only the attempt that committed, and the split is all or nothing.
  `database/outboxstore/e2e_test.go` runs a change from the service through the
  runner and the worker to the mail.
- **Webhooks and formats**: `server/notify/webhook_one_delivery_test.go`. One
  delivery for `*` and for several names, the exact body for a single name, no
  payload on the two new names, the retry recomputing the name, the Also line,
  reply text and status name rules, and old-shape rows.
- **Staff and requester mail**: `server/notify/split_test.go`. The first reason
  whose switch is on, the actor skip, the routed creator, one mail per address,
  a failed lookup retrying the row, a failed insert leaving nothing queued.
- **No breaking change in existing tests.** Tests that asserted a sequence of
  separate events were rewritten to assert the single merged event (the
  resolve-as-duplicate, reopen and priority tests, and the by-type event
  lookups, now by kind). `TestChangePriority_DispatchesNoNotification` pinned
  the opposite of ruling 2 and was replaced.

#### 10. Rulings

**Ruled by Erik, 2026-10-15** (the design review):

1. **The outbox rows are written inside the change's transaction.** Yes.
2. **Event names are the same whichever button made the change.** Yes: Resolve
   and Close also carry `status_changed`, and a `PATCH` to Resolved or Closed
   also carries `resolved` or `closed`. Priority and category changes now
   produce events.
3. **`ticket.priority_changed` and `ticket.classified` are names a hook can
   subscribe to**, with no priority or category id in the webhook body.
4. **The webhook body is unchanged.** The event inside the server carries the
   kinds, the from and to of each field, and the ticket state, so that
   subscriptions and filters can match on them. The body sent does not gain them.
   There is no version flag.
5. **Auto-assignment moves into the create transaction.** Yes.
6. **A hook subscribed to several names, or to `*`, gets one delivery per
   change, labelled with one name: accepted** (option b of three). A requester's
   reply that also reopened the ticket arrives as `ticket.reopened` only. The
   body does not list the other kinds. The upgrade notes tell such receivers to
   read the ticket through the API when they need more than the label. Rejected:
   (a) a list of names in the body; (c) one delivery per subscribed name, which
   brings back the duplicates.
7. **The tracking number stays out of the raw body** (reaffirms the 2026-10-13
   #448 ruling). The raw body is exactly today's.
8. **One event per ticket.** A change that touches two tickets produces two
   events. A follow-up gives one event for the new ticket and one `linked` event
   for the original.
9. **Build order.** One PR on top of #504, built after #503
   and #504 merge, with the upgrade notes in the same PR, then an Opus review.

**Ruled by Erik, 2026-10-16** (from the build and its review, recorded as
comments on #506):

10. **The Also line names only the kinds the hook subscribes to.** A `*` hook
    sees every other kind. A hook subscribed to a single name gets no Also line
    and no Jira `also` key. The line must never tell a channel about something it
    did not subscribe to.
11. **A creator who is auto-routed their own ticket gets one email.** Routing
    picked the assignee, so "no mail about your own action" does not suppress it.
    Mail is deduplicated by recipient address, so each address gets one mail per
    change. In Erik's words: "if both actions resolve to the same email address
    we dedup. one email". (Which mail is kept is a **build decision**, not a
    ruling of Erik's: the staff mail over the requester's, and between two staff
    accounts the stronger reason.)
12. **Reply text in chat and ITSM messages goes only to hooks subscribed to
    replies.** A hook on `ticket.reopened` only gets its "Reopened" message
    without the reply's text. Reply text appears only for hooks subscribed to
    `ticket.replied` or `*`. Internal notes are never included.
13. **A status change to the same status announces nothing about status.** A
    re-resolve of an already-Resolved ticket carries `resolved` only, and only
    the Resolve button with notes sends it. A `PATCH` that sends the same status
    plus an assignee carries `assigned` only. 1.2.x announced `status_changed`
    here, so the upgrade notes say so.

**Ruled by Erik, 2026-10-16.** Recorded on #506.

14. **Chat headlines for the two new events are readable.** `ticket.priority_changed`
    is "Priority changed" and `ticket.classified` is "Reclassified". (That the
    headlines name no value or name is a **build decision**, not a ruling of
    Erik's: values are content, as for the rest of the event.)
15. **A failed lookup of followers or group members retries the change.** No mail
    goes out until every recipient is known; nothing is queued from a partial
    answer.
16. **An assignment that changes nothing announces nothing.** That covers the
    same assignee, or a clear on a ticket that is already unassigned. It changes
    1.2.x behaviour, so it is in the upgrade notes. (Two details are **build
    decisions**, not rulings of Erik's: that "same" means the same user and the
    same group, and that the audit entry is still written, as for a same-status
    move.)
17. **An auto-routing assignment names nobody as the actor.** A hook told
    `ticket.assigned` gets `ActorID: null`, as in 1.2.x and as the audit log
    already records. The `ticket.created` delivery still names the creator.

No other questions are open.

That staff mail recipients are decided by the worker, a moment after the commit,
follows from ruling 1.

---

## Custom Fields

Admins can attach arbitrary structured data to tickets beyond the fixed fields (subject, description, priority, CTI).

### Field Definitions

Defined globally under **Admin → Custom Fields**. Each definition has:

- **Name** (unique)
- **Type**: `text`, `textarea`, `number`, `select`
- **Options** (select only): list of allowed values
- **Sort order**: controls display order
- **Active flag**: field defs are never hard-deleted — only deactivated. Deactivated fields do not appear on new ticket forms but existing values are preserved for history.

### Assignment to CTI Nodes

Fields are assigned to CTI nodes (category, type, or item) from the CTI editor (**Admin → Categories**). Each assignment has:

- **Visible on new**: whether the field appears on the new-ticket form
- **Required on new**: whether the field must be filled before the form can be submitted
- **Sort order**: display order within the node

The fields available on a ticket are the union of all fields assigned to its selected category, type, and item, ordered by scope level (category → type → item) then sort order within each level.

An assignment goes with the node it is attached to. Deleting a category, type or item deletes its assignments in the same statement (a category's include those of its types and items), so a delete that is refused for tickets in use leaves them alone. `custom_field_assignments.scope_id` has no foreign key, which is why this is done in the delete itself. Assignment routes check the whole path: the item must belong to the type, the type to the category, and the assignment to the scope the path ends at; a mismatch, or a scope that does not exist, answers `404`.

### Values

Stored normalized in `ticket_custom_field_values` (one row per ticket + field def, `value TEXT`) for filterability — not as a JSON blob. Values are edited with `PUT /tickets/{id}/custom-fields`, restricted to fields the ticket actually has (those assigned to its category, type and item, for every role). Staff and admin can edit at any time after creation, from the ticket detail page; a reporting user can edit the fields on their own ticket until it is Closed, and is refused with `409 ticket_closed` after (Closed is read-only to every requester).

Guests are shown no custom fields at all. The guest endpoint accepts none — a deliberate choice, since what an anonymous visitor may write into an operator's own fields is the operator's decision — and the public form no longer offers them. It did offer them for a while and threw the answers away on submit, which also meant a field marked required could stop a visitor filing a ticket at all. The field endpoints do not filter by role: the fields a ticket has are resolved from its own category, type and item, and `PUT /tickets/{id}/custom-fields` accepts any of them from any role that may write to the ticket. What a reporting user is offered follows from what they can pick (category and type, never an item), so in practice they see category and type fields, while staff and admin, who can pick all three, see all levels.

---

## CTI-Linked Group Management

Admins can manage which groups handle each CTI node directly from the CTI editor (**Admin → Categories**), without navigating to the Groups page.

The same editor archives and restores a category, type or item (#446): an archive button on each row sends `active: false`, after a confirmation that says requesters will no longer see it or be able to file new tickets under it; a restore button sends `active: true` with no confirmation. Archived rows are greyed and labelled **Archived**. Deleting an entry that tickets use is refused with `409` (`category_in_use`, `type_in_use`, `item_in_use`), and the dialog then suggests archiving instead.

- Each expanded **Category** row shows a **Groups** subsection listing groups assigned at the category level (type_id = NULL in `group_scopes`).
- Each expanded **Type** row shows a **Groups** subsection listing groups assigned to that specific category + type pair.
- **Items do not have a group management section** — items do not factor into scope per the scope model.
- The "Add group" dropdown shows active groups not already assigned at that node.
- Adding or removing a group here is equivalent to using the Groups page; both update the same `group_scopes` table.

---

## Canned Responses

Staff insert reusable reply templates into ticket replies with one click, so common acknowledgements, fixes, and closure messages don't have to be retyped.

### Definitions

Defined globally under **Admin → Canned Responses**. Each canned response has:

- **Name**: short label shown in the picker
- **Body**: plain text, inserted verbatim into the reply
- **Scope**: one of `global` (every ticket), `category` (any ticket in that category), or `category + type` (only tickets matching that category and type)
- **Sort order**: controls display order in the picker

Scope is stored as a nullable `category_id` plus a nullable `type_id`, following the same "category, optionally narrowed by type" model as `group_scopes`. A `type_id` is set only when `category_id` is also set (enforced by a CHECK constraint); both NULL means global. Item-level scoping is not supported, matching the group-scope model.

Canned responses are hard-deleted, not deactivated: the body is copied into the reply at insert time, so removing a template never affects replies that already used it. Deleting a category or type cascades to the responses scoped to it.

### Storage

Stored in a single `canned_responses` table (`id`, `name`, `body`, nullable `category_id`, nullable `type_id`, `sort_order`, `created_at`). There is no per-ticket linkage — once inserted, the text is part of the reply like any other typed content.

### Use in the reply composer

When composing a reply, staff see an **Insert canned response** control that opens a searchable picker. The picker lists the responses available for that ticket — global responses, responses scoped to the ticket's category, and responses scoped to the ticket's category and type:

```
category_id IS NULL
  OR (category_id = <ticket category> AND (type_id IS NULL OR type_id = <ticket type>))
```

Selecting a response splices its body into the reply textarea at the cursor position (appended to the end when empty). The inserted text is fully editable before the reply is sent.

### Permissions

Only admins create, edit, and delete canned responses; all staff and admins can insert them when replying. Allowing non-admin roles to manage templates — and assigning that capability per user or per group — depends on the custom-role work and is deferred to v3.

### Deferred

- Per-user / per-group "manage canned responses" capability → v3, with custom admin-defined roles.
- Variable substitution (e.g. `{{customer_name}}`), rich-text bodies, and usage analytics → a later version.
- Item-level scoping → not planned, mirroring the group-scope model.

---

## SLA Tracking (v1)

This section describes what runs. SLA tracking is off until an operator turns
it on from **Admin → Settings → Features**. The setting is read live, so the
toggle takes effect without a restart.

`SLA_ENABLED=true` in the environment switches that setting on at **every**
start, not only the first. It only ever switches it on and never off, so an
unset variable cannot disable a feature an administrator enabled — but an
administrator who turns the toggle off while the variable is still set will
find it on again after the next restart. Treat it as "this instance has SLA
tracking" rather than as a default.

### SLA Policies

When the SLA toggle is enabled, a **SLA Policies** management blade appears directly in the Features settings tab. Policies define the maximum time from ticket creation until a first response and until resolution. Each policy has:

| Field | Description |
|-------|-------------|
| **Name** | Display label, e.g. "Critical — 1h response" |
| **Priority** | Optional. `critical`, `high`, `medium`, or `low` — restricts the policy to tickets of that priority. Leave blank for "Any priority". |
| **Category** | Optional. Restricts the policy to a specific category. Leave blank for "All categories". |
| **Response target** | Minutes from ticket creation to the first staff reply visible to the reporter (internal notes do not count). A resolution also counts as the first response — see below. |
| **Resolution target** | Minutes from ticket creation to ticket resolved |

**A resolution counts as the first response**
([#219](https://github.com/PubliciaLLC/go-help-desk/issues/219)). If a
ticket reaches Resolved, or Closed without passing through Resolved,
before any staff reply, its first response is recorded at the resolution
instant, in a single statement on the ticket's SLA record (separate from,
and after, the ticket's own resolution write; a failure there is repaired
on the next resolve/close). The response target is judged against that
instant, so a ticket resolved without a reply is never left reporting a
response it can no longer receive. A ticket that already had an earlier
staff reply keeps that reply as its first response.

**Deleting a policy.** A policy that any ticket has been tracked against
cannot be deleted: those tickets' targets and breach stamps are measured
against it, and that includes a record a priority change has since superseded
(see Re-matching when priority changes). The API refuses with 409, naming how
many tickets depend on it. A policy that has never matched a ticket can be
deleted freely.

### Policy Matching

When a ticket is created, the system selects an SLA policy by specificity:

1. Priority + Category — most specific
2. Priority only
3. Category only
4. A catch-all (no Priority, no Category)
5. No SLA — if no policy matches

#### Re-matching when priority changes

A ticket can change priority after it is created (staff and admin,
`PATCH /tickets/{id}`, #443). The SLA policy is then matched again, by the same
four tiers, against the new priority and the ticket's category, and the change
is audited. **A staff change never restarts a clock.** Only a reopen does: see
**A reopen restarts both clocks** below.

**Resolved and Closed tickets are refused.** A priority change on either answers
`409 ticket_closed`, the same refusal every other write to a closed ticket gets,
and changes nothing: not the priority, not the SLA. The check is made on the
locked row. `PATCH /tickets/{id}` also asks it before it saves anything else in the request
(role first, then the closed state), so a status or assignment sent with the
priority is not left behind by the refusal.

**A record's facts are never rewritten.** A ticket can have a history of SLA
records, of which at most one is *current*. A re-match does not edit the current
record: it marks it **superseded** (`sla_records.superseded_at`, the only column
ever written on it again) and inserts a new record. Everything that addresses
"the ticket's SLA" (the indicator, the breach sweep, the recording of a first
response or a resolution) reads the current record only. A superseded record is
history: it is never evaluated, and its breach stamps stay as they were.

What a staff change does, decided in this order:

| The ticket and its current record | Outcome | The new record |
|---|---|---|
| SLA tracking is switched off | `sla_off` | none; nothing is re-matched |
| No record, no policy matches | `none` | none |
| No current record, a policy matches | `attached` | a record whose clock **resumes where the last one ran**: it takes the `started_at` and pause base of the ticket's most recently superseded record, and none of its met or breach facts. A ticket that never had a record starts **at its creation**, not at the change, and can already be breached when it is created |
| The new priority matches the record's own policy | `unchanged` | none |
| Another policy matches | `rematched` | replaces the old record under the new policy, **keeping its clock** (so elapsed time still counts) and **its facts** (so a target already met stays met) |
| A record exists, no policy matches | `removed` | none; the record is superseded and the ticket shows no SLA |

**Moving through a priority with no policy does not undo a restart** (Erik,
2026-10-14). A priority with no matching policy supersedes the record and leaves
the ticket with none (`removed`). When a later change gives it one back
(`attached`), the new record resumes from the **later of** the ticket's last
reopen (`last_reopened_at`) and the newest superseded record's start, with the
pause base that goes with whichever wins, instead of the ticket's creation. So a
restart is not thrown away by a trip through a priority with no policy, a reopen
that itself lands on a priority with no policy still counts as a restart (a
ticket reopened onto high, which has no policy, and moved back to medium resumes
from the reopen, not from before it), and the time spent there still counts.
`sla_restarted` is written when a reopen supersedes a record, even if none
starts, because the old clock did end and the restart point is recorded on the
ticket; it is not written when the ticket had no record to replace, even if the
reopen starts a first one (Erik, 2026-10-15). It gains
nothing, and it is not punished beyond what happened: the pause base comes with
the start, and the record's facts (met, breached) are not copied. This applies to
a staff change only; a reopen always starts at the reopen. The "SLA restarted"
badge reads the same as before: the record the restart replaced.

**A reopen restarts both clocks** ([#464](https://github.com/PubliciaLLC/go-help-desk/issues/464)).
Whenever a ticket is reopened (a requester's reply, a status change out of
Resolved or Closed, or `POST /reopen`: Ticket Lifecycle), the reopen's own
transaction calls the same primitive with the reason `reopened`, after the status
change. That reason always starts both clocks over from the reopen: the current
record is superseded and kept, and a new one starts at the reopen with no facts,
so both targets are timed anew. It does so on **every** reopen, not only the
first (the priority bump is first-only; the restart is not, Erik 2026-10-14), even when the
priority does not change (a ticket already at the highest priority cannot be
raised, and a later reopen does not raise it) and even when the new priority maps
to the same policy. It is not refused for being Resolved or Closed, because the
reopen has just moved the ticket out of it in that same transaction. With SLA
tracking switched off no record is created. The audit feed carries
`priority_changed` (when the priority changed, with the outcome) and
`sla_restarted`. A record that started over is recognisable as one (`started_at`
is set), and because it is a clean clock it can clear a breach from the
indicator; the breach stays on the superseded record, and `sla_restarted` says
when.

**"SLA restarted" badge.** When the clock really restarted, the ticket page
shows an **SLA restarted** badge next to the SLA indicator (staff and admin; a
reporting user sees no SLA). It exists because of a reopen and nothing else: a staff or admin priority change
**never makes it appear on a ticket that was never reopened**, and never
restarts a clock itself; it re-matches the SLA and keeps its clock, whatever the
old record had already met or breached. (On a ticket whose reopen landed on a
priority with no policy, the badge first shows at the staff change that gives it
a record back: that record resumes from the reopen, so it is the reopen's
restart showing, not the priority change's.) It appears on a reopened
ticket whose earlier record had something met or breached. Its tooltip reads
"The SLA clock restarted when this ticket was reopened. The earlier record is
kept; select to see it." Selecting it
opens a small panel (Escape closes it) with the earlier record's facts: its
policy, when its clock started, when it was replaced, and whether it ended met or
breached. `GET /tickets/{id}` carries it as `sla.earlier`; a list row never does.

What tells a restart from a re-match is the clock start, not the stamps: a
re-match copies the old record's `started_at` and its met and breach stamps onto
the new record, so the stamps are the same in both and say nothing. Only a
restart gives a record a `started_at` of its own. The current record's
`started_at` is the reference: empty means the clock runs from the ticket's
creation, so it cannot have restarted; otherwise the newest superseded record
whose clock started somewhere else is the one the restart replaced (a re-match
after a restart shares the restart's start and is passed over, so the badge stays
after one, correctly, because the clock did restart). If that record ended with
nothing met and nothing breached there is no outcome to show and no badge. The
outcome reads the record's stored stamps, so a breach the sweep has not stamped
yet is not reported.

**The clock.** A record's clock starts at its `started_at`, or at the ticket's
creation when that is empty (every record written before this existed, every
record a new ticket attaches, and a record a staff priority change attaches to a
ticket that never had one).
A record also stores `paused_seconds_at_start`: the ticket's pause total plus
the part of an open Pending interval already elapsed, as of its start, so pause
time before a restart does not count toward the new record. A re-match carries
both onto the record that replaces it.

**Atomic.** The priority, the SLA rows and the audit entries are written in one
transaction, like the ticket service's other composite writes: an audit entry
committed apart from the change it describes is not an audit trail. (A new
ticket's SLA record, by contrast, is attached after the ticket commits and is
best-effort.) A second change made at the same moment waits for the first, and
the schema allows one current record per ticket.

**Gated on the toggle.** A re-match creates records, so, like attaching a policy
to a new ticket, it happens only while SLA tracking is on. Recording a first
response or a resolution onto an existing record is not gated.

**Event.** A priority change that moves the priority raises `ticket.priority_changed` (written in the same transaction; no event when the priority is unchanged). It sends no email. See [Ticket Events: One Change, One Event](#ticket-events-one-change-one-event).

**Known limit.** Recording a first response or a resolution
(`RecordFirstResponse`, `RecordResolved`) runs after the reply's own commit and
outside the ticket's row lock. If a re-match commits between that code's read of
the record and its write, the write finds no current row to land on (the old one
is superseded, and the new one is invisible to a statement that started earlier),
so that one response is not recorded on the new record. For a reopen's restart
that is correct, because the response or resolution came before the new clock
(it is the superseded record that misses it). For a staff re-match the new
record keeps waiting for a response it should have had. The breach sweep has
the same window, but it heals itself: the new record is evaluated on the next
pass. The window is
milliseconds. Closing it would need the write to report rows affected and the
recording code to retry once, which changes the store signatures; it is left
until there is a real report.

### Timer Mechanics (pause / resume)

A target is measured against **elapsed time since creation, minus time spent
Pending** — not wall-clock time since creation. That subtraction is the part
that has to be modeled explicitly, because "paused" is a duration a ticket
accumulates across possibly several Pending intervals, not a single flag:

- "Pending" means the status **named** `Pending`: a seeded *custom* status,
  not a system one, matched by name (`ticket.StatusNamePending`). There is no
  per-status "pauses the SLA" flag in v1. Renaming the status away from
  `Pending` stops tickets from pausing from then on. A ticket already in it
  stays paused until it leaves, since leaving closes the interval whatever the
  status is called. Deactivating it stops tickets entering it: the
  server refuses a move into an inactive status for every role and the
  ticket-detail status picker no longer lists it. A ticket already in it stays
  paused until it leaves. A reopen cannot enter it either: an inactive reopen
  target falls back to New. Deleting it removes the only way to pause. Whatever
  status is later named `Pending`, renamed back or newly created, becomes the
  pause status.
- Each time a ticket enters Pending, that timestamp is recorded as the start
  of a paused interval; each time it leaves Pending (to any other status), the
  interval closes and its length is added to the ticket's accumulated
  `tickets.sla_paused_seconds`.
- **Elapsed-toward-target**, at any instant, is `now - created_at -
  sla_paused_seconds` (for a record a reopen has started over, and any record
  that later replaced it, from the record's own start and pause base instead: `now - started_at -
  (sla_paused_seconds - paused_seconds_at_start)`; see Re-matching when
  priority changes), with one adjustment: if the ticket is *currently*
  Pending, the still-open interval's length (from its start to now) is added
  on top, so a ticket does not appear to be making progress toward breach
  while it is actively paused.
- A response or resolution that lands while paused stops that clock the same
  way a status change would: `sla_records.first_response_at` / `resolved_at`
  is a timestamp, not a running total, so once it's set the elapsed-time
  formula above is irrelevant to it — only breaches still open at that
  moment continue to accrue pause time.
- Re-entering Pending after a reopen (see Ticket Lifecycle) resumes
  accumulating against the same accumulated pause total; the pause time before
  a reopen does not count toward the restarted record (its base is the pause
  total as of the reopen), so a reopen's new record is a new SLA clock.

### Breach Evaluation

Because elapsed-toward-target keeps moving without any action on the ticket, a
breach can occur while nobody touches it — so, like ticket auto-close, this
cannot be computed only on read. A periodic background sweep evaluates every
open ticket with an attached policy and no existing breach timestamp for a
target still outstanding, and stamps `response_breached_at` /
`resolution_breached_at` the first time elapsed-toward-target exceeds the
policy's minutes for that target. The stamp, once set, does not clear itself
if a ticket is later reopened or its policy changes — a breach that happened
is a fact about what happened, not a live status. A priority change that
re-matches the SLA does not clear it either: the breach stamp is carried onto
the record that replaces it, and the superseded record, which is never evaluated
again, keeps its own. (A reopen's restart starts a clean record; the stamp stays
on the superseded one.)

The sweep runs every minute from a goroutine started in
`cmd/server/main.go` and calls `sla.Service.SweepBreaches`. That selects
candidates with a pause-aware prefilter query (`ListSLABreachCandidates`)
and re-checks each against a fresh read of the ticket before stamping.
One minute because targets are whole minutes and a sweep stamp records
when a breach was *detected*, so the interval is the stamp's worst-case
error. The goroutine always runs, but each tick first reads the SLA toggle
and does nothing while it is off. A failure on one ticket is logged and
does not stop the pass.

The sweep is not the only writer. When a first response or a resolution is
recorded (`RecordFirstResponse` / `RecordResolved`), the same statement that
stores the timestamp also stamps that target's breach if it was already
exceeded at that instant. A late response or resolution is therefore
stamped when it happens, even between sweep ticks. These record-time writes
are not gated on the SLA toggle: a ticket that already has an SLA record
gets its facts recorded truthfully while the feature is off. The toggle
governs whether new tickets get a record and whether the sweep runs. Every
writer is write-once per column (the first stamp wins, and none overwrites
a stamp already set).

Timer mechanics were tracked as
[#181](https://github.com/PubliciaLLC/go-help-desk/issues/181), the scheduler
as [#182](https://github.com/PubliciaLLC/go-help-desk/issues/182), and the
queue indicator below as
[#183](https://github.com/PubliciaLLC/go-help-desk/issues/183).

### SLA Indicators

The ticket queue shows a color-coded SLA indicator per ticket, computed from
the same elapsed-toward-target calculation used for breach evaluation (not
from the breach stamp alone, since amber has to render before a breach
timestamp would ever be set):

- **Green** — elapsed-toward-target is under 80% of the applicable minutes
- **Amber** — elapsed-toward-target is at or past 80% but under 100%
- **Red** — elapsed-toward-target is at or past 100% (breached), whether or
  not the background sweep has stamped it yet — the stamp is for reporting and
  for freezing a fact in time, the color is a live read
- A ticket with no matching policy shows no indicator at all, not green.

The response and resolution targets are indicated separately when both are
outstanding (e.g. resolution can be amber while response is already breached);
once a target's timestamp (`sla_records.first_response_at` / `resolved_at`)
is set, that target's indicator stops updating and shows its final color.

**Visibility:** the indicator, the policy name, the targets, and the
breach/late status are staff/admin only. A reporting user's own ticket never
carries any of it — the API returns `"sla": null` on that response regardless
of whether a policy is attached — so a customer cannot see how their ticket is
being timed or that it has breached.
