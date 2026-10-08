# Go Help Desk — Design Document

## Overview

Open-source, self-hosted help desk system inspired by HESK, with SAML authentication, a plugin infrastructure, and a REST API. Built with a long-term roadmap toward SaaS (v4).

## Versioning Roadmap

| Version | Scope |
|---------|-------|
| **v1** | Core ticketing (with linked tickets, optional SLA tracking), local + SAML auth + MFA, custom fields, CTI-linked group management, canned responses, full-text search (Postgres FTS), REST API, MCP interface, email + webhook notifications (with Slack/Teams/Discord/JIRA payload formats), Docker deployment |
| **v2** | Plugin system (1st/3rd-party, sandboxed, admin UI install) |
| **v3** | Reporting, knowledge base, custom admin-defined roles |
| **v4** | Multi-tenancy / SaaS, plugin registry, ITSM ticket types (Incident/SR/Problem/Change), Impact × Urgency priority matrix, default ticket type per CTI |

**1.3 note:** Custom fields, CTI-linked group management, and canned responses were
built ahead of the original v2 schedule and are already in production — this
table reflects that rather than the sequence they were originally planned in.
Full-text search was likewise already implemented as core v1 functionality;
see Ticket Search below. Plugins move the other direction: the admin UI lists
and toggles a plugin record, but install/uninstall return `501` and no event
ever reaches a plugin (`plugin.Registry.Dispatch` is built but never wired into
the notification chain) — there is no working plugin system today, so it is
rescheduled to v2 rather than left claiming v1 status it doesn't have. The one
piece of the original plugin pitch worth keeping on the v1 timeline — chat/ITSM
notifications — ships as formats on the existing webhook feature instead of
through the plugin system; see Notifications below.

---

## Tech Stack

| Layer | Choice | Rationale |
|-------|--------|-----------|
| Backend | **Go** | Single binary, small Docker image, strong concurrency, good plugin/WASM story |
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
Category is required on every ticket, and statuses are seeded by migration
while categories were not — so a freshly set-up instance had none, and the
first ticket its new administrator tried to file answered
`400 category_id is required`. The error named a field rather than the action
needed, nothing on the way in said "create a category first", and setup does
not reopen. The whole test suite was blind to it because the harness seeds a
category of its own, so the state a real first run is in was never exercised.

`POST /setup` therefore takes an optional `category` name alongside the
administrator, and the wizard asks for it with a sensible value already
filled in. The category is created **before** the administrator and only when
none exists: setup answers 409 forever once a user exists, so a failure
between the two steps must not be able to leave an instance with an
administrator and no category. That ordering leaves two failure modes, both
retryable — nothing happened, or a category exists and no user does — and the
existence check is what makes the retry reuse it instead of stacking
duplicates. See #323.

### Tickets (v1)

Core fields (all editions):

- **Subject** (required, short summary)
- **Description** (required, full detail of the request/issue)
- **Category** (required)
- **Type** (optional, depends on Category having Types defined)
- **Item** (optional, depends on Type having Items defined)
- **Priority** (configurable levels, e.g. Critical / High / Medium / Low)
- **Status** (customizable statuses per instance, see Ticket Lifecycle below)
- **Assignee** (staff member or group)
- **Attachments** (file uploads)
- **Replies/thread** (staff and user messages)
- **Linked tickets** (related, parent/child, caused-by, duplicate-of — can link to any ticket including Closed)
- **Tracking number** (quoted in notification email, and one half of a guest link re-request)
- **Resolution notes** (summary of what resolved the ticket, captured at resolution)

SLA fields (optional feature toggle, all editions):

- **SLA target** (response time and resolution time targets, configurable per Priority and/or Category)

ITSM fields (v4 SaaS only):

- **Ticket Type** (Incident, Service Request, Problem, Change Request)
- **Impact** (High / Medium / Low — how broadly the issue affects the organization)
- **Urgency** (High / Medium / Low — how time-sensitive the issue is)
- **Priority** (overridden: derived from Impact × Urgency matrix instead of manual selection)
- **Default ticket type per CTI** — when ITSM is enabled, each Category/Type/Item combination can have a default ticket type configured (e.g. `Hardware > Laptop > Broken Screen` defaults to Incident)

### Users & Roles

Three roles: **Admin**, **Staff**, **User**

| Role | Capabilities |
|------|-------------|
| **Admin** | Full system access. Manage settings, users, groups, categories, plugins, tags. Can always log in with local auth even when SAML is enabled (failsafe). |
| **Staff** | Create tickets. View/edit/assign tickets within their scope. Search tickets by tracking number, subject, or description keywords. Jump directly to any ticket by tracking number or UUID. Assign tickets to any staff member or group. Add and remove tags on tickets. |
| **User** | Create tickets. View their own tickets. Reply to and attach files to their own tickets until they are Closed; a Closed ticket is read-only to them (see [Closed is terminal](#closed-is-terminal-and-read-only)). Reopen a Resolved ticket by replying within a configurable window (admin setting: "Users can reopen tickets for X days after resolution"). |

**Custom admin-defined roles (v3):** admins will be able to define additional roles and grant a curated set of permissions (e.g., "Tier 1 Agent" with ticket read/reply but no assignment rights). The three built-in roles remain as defaults and cannot be removed. Permissions are stored as discrete capability flags rather than hardcoded in code, with the built-in roles expressed as preset bundles so existing behavior is preserved.

### User Management (Admin)

Admins manage accounts from **Admin → Users**. The user list is clickable — clicking a user opens a detail page with:

- **Profile** — edit display name, email address, and role. Changes take effect immediately.
- **Account info** — member since date, login type (Local / SSO / Local + SSO), MFA enrollment status.
- **MFA reset** — clears every second factor the user holds (the authenticator and all registered passkeys) and ends all of their sessions, so the user re-enrols on next login. Only shown when the user holds any second factor.
- **Enable / Disable** — disabled accounts cannot log in. Tickets and history are preserved. Re-enable at any time.
- **Password reset** — set a new password directly (shown only for accounts with a local password). No email link required for admin-initiated resets.
- **Groups** — view current group membership, add to groups, or remove from groups.
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
- **Reopen target status**: the status a ticket is moved to when it is reopened. Configured from **Admin → Settings → General → Ticket lifecycle → Reopen target status** (a picker limited to active, non-system statuses). Defaults to the status named "New" when unset, not to the first active custom status.
- **Closed reopen policy**: admin setting (`closed_reopen_policy`, shown as **Reopening closed tickets** under Admin → Settings → Ticket lifecycle; session-gated like the other settings that widen who may do what). `off` (the default) | `admin` | `staff_admin`. **Closed is terminal by default; an instance setting can allow forced reopen by admins only, or by staff and admins; requesters never.** Unset, unreadable or unrecognised reads as `off`, never as a permissive value; an unrecognised value is refused (`400 invalid_closed_reopen_policy`) when saved. Read on every request, never cached, so a change applies to the very next request.
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
role (status, assignment, reclassification, resolve, close, tags) keep their
`403`; the closed state does not change which refusal a role gets.

**Staff and admin: the minimal reading.** Only *leaving* Closed is removed.
Replying (including internal notes), assignment, reclassification, tags,
linking and attachments are unchanged on a closed ticket, because they do not
change what state the ticket is in, and removing them is not what was decided.
(A staff reply on a closed ticket still mails the guest a read-only link, below.)
Over MCP `update_ticket_status` answers a move out of Closed with the closed
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
  **reactivated**. They can only be **deleted** when no ticket is in that
  status and no status-history entry references it; otherwise deactivate
  it. System statuses can never be renamed, deactivated or deleted.
- Every status transition is recorded in a **status history** timeline and displayed on the ticket detail page interleaved with replies, in chronological order. Events include: the old and new status names (with colors), who made the change (user display name or "System" for auto-close), and the timestamp. The initial status assignment at ticket creation is also recorded.

### Ticket Activity Feed

`GET /api/v1/tickets/{id}/audit` (#129) answers "who changed this ticket, and when" from the audit log the domain layer already writes, without database access. Shown on the ticket detail page next to the status timeline, staff and admin only — a UI choice, not a new permission: the route inherits `requireTicketAccess` like every other route under `/tickets/{id}`.

This route only *reads* the audit log — it does not change what the domain layer writes to it. What gets written is narrower than "every mutation": `created`, `status_changed`, `assigned`, `resolved`, `closed` and `reopened` (a requester's reply that reopens a Resolved ticket, and `POST /reopen`, which is a force-reopen allowed only by `closed_reopen_policy` (#349) and says so with `forced_reopen` and the policy in its "after") are covered; priority, CTI and custom-field changes are not, and a cleared assignment is currently recorded as another `assigned` entry rather than `unassigned` (`unassigned` is written only when a departing user's tickets are returned to the queue). Those are pre-existing gaps in what the domain layer records, not something this route introduces — tracked separately rather than fixed here, since closing them is a write-side change to `ticket.Service`, not a read-side one.

That inherited gate is enough for the entry itself (status, priority, subject, assignee are already visible on the ticket to anyone who can view it), but not for the *actor* on `assigned`/`unassigned` entries specifically: assignment is staff/admin-only, and unlike every other action here, nothing else on the ticket discloses that actor's identity to a reporting user — the ticket's own `assignee_user_id` is a bare UUID, and `GET /api/v1/staff`, the only place that resolves one to a name, is itself staff/admin-gated. So the API withholds `actor_id`/`actor_name` on those two actions when the caller is a plain reporting user, the same way `ticket.VisibleReplies` withholds internal-note authorship from that same viewer. Every other action's actor is fine to show as-is (see `StatusHistoryEntry.ChangedByName`, which already does, via `/history`).

The field-level before/after diff is a second, independent gate on top of the feed itself: admin always sees it; staff only when `staff_can_view_ticket_change_history` (an admin setting, off by default) is on; a reporting user never sees it, regardless of that setting. What is shown is redacted by field name (`audit.Redact`) — a denylist of plausible secret-field stems (`password`, `secret`, `token`, `hash`, …) plus any key ending in `key` or `pass` (so every write-only setting in `secretSettingKeys` is covered, and a test keeps it that way), matched with case and separators ignored (`passwordHash`, `API_KEY` and `api-key` all land) and applied at every depth of nested maps and lists, not anything actually written into a ticket's before/after today (`ticketMap` only ever carries `id`, `status_id`, `priority`, `subject`), kept ready for the other entity types the admin-wide view below can show.

### Admin-Wide Audit View

`GET /api/v1/admin/audit` (#129's remaining half) answers the same question across every entity, not one ticket at a time. Staff and admin only, same resource gate as the ticket subtree; a reporting user has no route here.

Staff are narrowed twice, independently. `entity_type` is forced to `ticket` whatever the query string asks for: every other entity type is admin-only. And with ticket scope enforcement on, only entries on tickets the staff member may see are returned.

That second narrowing is part of the query, not something applied to its result. One statement carries the filters (`entity_type`, `action`, `actor_id`, `from`/`to`, `q`) and the scope together, joined against `tickets`, and the count behind `total` applies the same filters and the same scope predicate, without the page (and bounded; see below). So the page, the offset and the total all describe one sequence: `total` is the number of entries the viewer can page through, and says nothing about the ones they cannot see. An earlier version took Search's own count before scope was applied in Go, which told a staff member how many entries existed on tickets they could not see — and with the actor, action and date filters this endpoint accepts, a count plus bisection dates activity on those tickets. Putting scope into the count's own `WHERE` closes that, and is also why staff now get a `total` like an admin does.

The scope predicate is the one the scoped ticket listing uses (`ListTicketsFiltered`): a ticket is visible to a staff member if they reported it, are assigned it, their group is assigned it, or it falls in a Category/Type a group of theirs covers. It is evaluated by the database from the caller's user id, so nothing is looked up per entry or per ticket and the request does not grow with the size of the log. There are still two rules — `ticket.CanView` and the SQL — and the SQL states its half twice, in the page query and in the count (the count finds the visible tickets first and then the entries on them, for the cost reason below; the conditions are the same ones). A test (`TestAdminAudit_ScopeParity`) holds all of them equal, page and `total` alike, across staff in and out of groups, category-level, type-level and combined rules, unscoped tickets, tickets the viewer reported or is assigned, administrators, enforcement on and off, and every filter, so a change to one that is not made to the other fails it.

Non-ticket entries and entries whose ticket no longer exists are never returned to a scoped staff member. To the person asking, an entry on a ticket they may not see and an entry on a ticket that does not exist are the same thing: both answer with nothing, so the view cannot be used to learn which ticket ids exist — the same policy as `404` for a ticket the caller may not see (see the **Authorization** paragraph under "MCP Interface", which also records the REST `404` change, #174). Entries with the same timestamp are ordered by id as well, so the order is exact. Both views page by offset, so new entries arriving between two page requests shift what the next page starts on — as with any offset pager.

**What bounds the cost.** The page is one statement that reads `limit + 1` rows; the extra row is how `has_more` is known, so paging never depends on the count. The count is bounded: it stops after 10,001 matches (`audit.TotalCap`, 10,000). `total` is exact up to and including 10,000 entries; beyond that it is 10,000, `total_capped` is `true`, and the page shows "10,000+". `total_capped` is always sent, `false` included, and a total that merely equals the cap is `false`. Nothing else changes for an instance under the cap: the same exact number, the same order, the same pages. Admin and staff go through the same statements, so the cap applies to both and staff are no more or less capped than an admin who sees the same entries. #331 is why: retention is off by default, so the table only grows, and an exact `COUNT(*)` over it is a full scan on every page view.

Measured with `EXPLAIN ANALYZE` on a rolled-back seed of 524,000 entries and 30,000 tickets (local PostgreSQL 16, one or two runs each, the statements as the server prepares them): the unfiltered admin count went from about 45 ms to about 2 ms, an `action` filter matching 60% of rows from about 45 ms to 3 ms, and a `q` filter matching a third of them from about 150 ms to 23 ms. The staff count, which before had to walk the whole log joined to `tickets`, took 330–1,400 ms (it varied between runs and machines) for a staff member who sees a tenth of the tickets, a handful, or all of them, and takes 30–190 ms now, the high end being the staff member who sees only a handful, whose matches are rare enough that the count reads most of the log. Those are the cases where matches are plentiful and the cap lets the count stop early. Limits to know about, rather than discover:

- A filter that matches few rows still has to read the table to find that out. A `q` that matches nothing took about 137 ms before and after, because `q` is a substring match no existing index serves. A rare `action` (about 500 entries) took 0.8 ms on the existing index. No index was added: the filters that matter (`action`, `actor_id`, `created_at`) already have one, `q` is a substring match a b-tree cannot serve, and the staff count's remaining cost is on the `tickets` side.
- The staff count builds the set of visible tickets before it touches the log, so it pays for that set even when its filter is narrow. The Category/Type rule is written as two `IN` lists so the set costs one pass over `tickets` (about 8 ms at 30,000 tickets when the viewer sees a tenth of them), not a subplan per ticket. With a narrow indexed filter (an `action` with about 500 entries, a ten-minute window of about 600) a staff count takes 5–12 ms for a staff member who sees a handful or a tenth of the tickets and 25–40 ms for one who sees all 30,000, where it took 3–4 ms before the count was bounded, because the old statement probed `tickets` only for the rows the filter let through. That is a known regression for that case, accepted because the alternative shape (probe per entry) made the staff member with few visible tickets three times slower (411 ms against 143 ms). It grows with the number of tickets, not the size of the log, and the cap does not shorten it. The admin count has no such cost: the set is never built.
- Under a generic (parameter-independent) plan PostgreSQL's JIT compiler can add several hundred milliseconds to the first runs of a large scan; that was seen on the pre-#331 count and noted in review of the new one. It is a server setting (`jit`, `jit_above_cost`), not a property of the statements, and the figures above leave it out.

The page query is unchanged. Its cost for a staff member who sees few tickets depends on how far back it has to look for a page of visible entries (about 240–290 ms for a staff member with five visible tickets on that seed, a few milliseconds for one who sees a tenth of them), and the cap does not bound it.

**Known, accepted side channel: response time.** Bulk hidden activity inside a date window is still detectable by timing: about 5,000 hidden rows in a window measured at roughly 12–13 ms for the page plus about 12 ms for the count, against about 1 ms for an empty window, while 20 hidden rows were indistinguishable from none. That is roughly two orders of magnitude smaller than the removed walk's signal and the same class as the scoped ticket listing's. It does not reveal which tickets exist or anything about their content, and the response itself (entries, `total`, `total_capped`, `has_more`) is identical for hidden and missing tickets. These timings were taken before the count was bounded and have not been re-measured; the note is unchanged.

With scope enforcement off — the default — staff may see every ticket, so no scope is applied: they get the admin's query, restricted to ticket entries, with entries on tickets that no longer exist included, since there is nothing to hide them from.

**Retention is opt-in, and off by default.** `audit_retention_days` (admin
setting) governs a daily sweep that hard-deletes anything older — no archive
table. The first sweep runs two minutes after start, not only on the
24-hour tick, so an instance restarted more often than daily still prunes; when
a window is set, startup logs it. **Unset, zero or negative means keep forever**, which is what every
release before this one did by having nothing prune at all.

That default is deliberate and is the opposite of what a first draft of this
feature shipped. A 365-day default would delete the first year of history on
any instance that had been running longer, one day after upgrading, from a
setting the operator never touched — the exact shape CLAUDE.md's "existing
behaviour must not change" rule exists to stop. An audit log is also the worst
thing in the system to shorten by accident: it is what you reach for after
something has already gone wrong, and it cannot be reconstructed.

A misconfigured value fails the same way. Anything unreadable or non-positive
is treated as forever, so the failure direction loses no evidence.

The upper bound is **36,525 days** (a Gregorian century, leap days included;
`admin.AuditRetentionMaxDays`). The settings endpoint refuses anything above
it, and a stored value above it — written before the check existed, or
directly in the database — is read as the cap, because a cutoff date computed
from a larger number can overflow into the future and delete everything.

`audit_retention_days` is **auth-critical** (`admin.AuthCriticalKeys`), so a
machine credential cannot change it. Shortening retention is the one setting
that destroys evidence rather than merely widening access: set it to 1 and
tomorrow's sweep removes every `mfa_reset` and `password_reset_by_admin`
entry, so a leaked API key that performed a credential reset could erase the
record of having done it. That is #306's own reasoning about `ResetMFA`
("the exact action an attacker would want unrecorded") applied to the record
rather than the act.

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
- Users only see results from their own tickets; staff/admin see results from tickets assigned to them and their groups.
- Reply bodies are not indexed in v2 — only ticket subject and description. Deferred: searching reply content, fuzzy/typo-tolerant matching, and per-user saved searches.
- Uses Postgres's `english` text search configuration, which drops common English stop words (e.g. searching just "IT" matches nothing) — an accepted tradeoff of FTS, not a bug.

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

### Groups & Scope

- A **group** is a named pool of staff members
- A staff member can belong to **multiple groups**
- Groups are scoped to **Category/Type pairs**:
  - A group can be assigned to specific Category + Type combinations
  - Or assigned to an entire Category (implying all Types and Items under it)
- **Items do not factor into scope** — staff in-scope for a Type see all Items under it
- Scope is derived **exclusively from group membership** (no direct Category assignment to individual staff)
- Solo admin scenario: assign all categories to a single group
- Staff members can see all tickets assigned to any group they belong to, and can take any action on those tickets

### Branding

- **Site name** — the product name shown in the sidebar header and browser title. Defaults to "Go Help Desk".
- **Logo** — uploaded via **Admin → Settings → Branding**. Accepted formats: PNG, JPEG, GIF. Max 2 MB. Images are proportionally scaled to fit within **320 × 64 px** and re-encoded as PNG. When set, the logo replaces the site name text in the sidebar. **SVG is not accepted** (#165): the logo is the only upload rendered inline in this origin, and pattern-matching SVG for scripts is a game you have to keep winning. An SVG logo uploaded by an earlier release stops being served after the upgrade — the route serves PNG and nothing else, so the sidebar falls back to the site name. The settings page says so where the logo would be: the file picker no longer offers SVG, a sentence explains why it went, and an instance whose stored logo can no longer be loaded is told that rather than shown an empty space.
- Both settings are stored in the database and managed via **Admin → Settings → Branding**.
- A public `GET /api/v1/site` endpoint returns `{name, logo_url, version}` — no authentication required, so the shell renders correctly before login.
- A public `GET /api/v1/logo` endpoint serves the stored logo file with a 5-minute cache header. `logo_url` in the site response points here when a logo is uploaded.

---

## Authentication

### Local Auth (Default)

- Username/password with bcrypt hashing
- Available for all roles by default
- **MFA** (optional toggle in admin settings): TOTP-based (Google Authenticator, Authy, etc.). When enabled, users enroll via QR code on next login. Admin can enforce MFA for specific roles or all users.

### Passkeys (WebAuthn)

A second factor alongside TOTP, and later an alternative to the password
itself. TOTP does not change and does not go away; an instance that upgrades
into this notices nothing until somebody registers a key.

**What this claims, and what it does not.** The property being bought here is
**phishing-resistance**: a WebAuthn credential is bound to the origin it was
registered against, so a convincing look-alike login page cannot use it. That
matters on a help desk because a staff account reads every ticket, every
attachment and every customer's details, and TOTP does not have this property
— a fake page collects the six digits and replays them inside the window.

It is deliberately **not** claiming "something you have" in the hardware sense.
A passkey today is very often a *synced* credential — iCloud Keychain, Google
Password Manager — which lives wherever that cloud account lives rather than on
one device. That is a weaker possession story than a YubiKey, and pretending
otherwise in this document would make an operator believe something untrue
about their own instance. Phishing-resistance holds for every credential this
accepts, synced or not; physical possession does not, so it is not claimed.

**Storage.** A `webauthn_credentials` table, not more columns on `users`: one
person registers several keys on purpose — a laptop, a phone, a spare in a
drawer — and that is the feature rather than an edge case. Each row holds the
credential id, the public key, the sign count, the transports, the AAGUID, the
backup-eligible and backup-state flags, a name its owner chose, and created and
last-used timestamps.

The name is optional. An unnamed credential is shown by what can be derived
from its transports and its age — "Security key, added 3 March", "This device,
added 3 March" for an `internal` authenticator — which is more use than a bare
date and leaks nothing the AAGUID would.

`credential_id` carries a **unique constraint in the schema**, not a check in
Go. The specification says credential ids are globally unique; "the
specification says so" is exactly the kind of claim this codebase puts a
constraint behind, and a read-then-insert has a window between the read and the
insert whatever it reads.

Three of those columns are worth explaining, because two of them are read by
nothing today:

- **`transports`** is not stored for a future screen. It goes back out on the
  sign-in challenge as `allowCredentials[].transports`, which lets the browser
  skip authenticators that cannot satisfy the request instead of prompting for
  every method the account has ever registered. It has a job from the first
  release.
- **`aaguid`** identifies the authenticator model. Nothing reads it yet. It is
  kept because it is free at registration and unrecoverable afterwards, the
  same reasoning that keeps the unused CIRCL response fields in
  `internal/reputation/circl.go`.
- **`backup_eligible` / `backup_state`** are the WebAuthn authenticator-data
  flags that say whether a credential is synced. They are the only way an
  administrator auditing this instance can tell a hardware key from an iCloud
  passkey — which is precisely the distinction the paragraph above turns on.
  Not captured at registration, the question is unanswerable forever after.

**Sign count is stored and not enforced.** The counter exists in the
specification for clone detection, but most modern authenticators return zero
always, and a naive "it must increase" rule locks those people out for nothing.
One case is worth noticing: a counter that was previously non-zero and then
goes backwards is a genuine clone signal with no false-positive cost. That is
logged and gates nothing — the same shape as `KnownMalicious` in `circl.go`,
where a field is decoded, recorded and deliberately never allowed to change a
verdict.

**Library.** `github.com/go-webauthn/webauthn`. The registration and assertion
ceremonies have many ways to be subtly wrong, and being exactly right is the
whole value of the feature. Nothing here is hand-rolled.

**Enrolment** follows the shape TOTP arrived at the hard way.
`POST /me/passkeys/register/start` mints a challenge and stages it **in the
session**; `POST /me/passkeys/register/finish` verifies the attestation and
writes the credential. Nothing is written to the account until the person has
proved they hold the key — the reason `GenerateMFASecret` and
`ConfirmMFAEnrollmentWith` replaced the older `EnrollMFA`, which wrote an
unconfirmed secret straight over the authenticator its owner was still using.

The staged challenge **expires, and the expiry is checked when the assertion
comes back**. A challenge left sitting in a long-lived session is a replay
window that stays open as long as the tab does. It expires on a fixed deadline
from when it was minted and does not slide forward on use, the same rule the
MFA lockout follows.

Both routes sit inside `meRouter`'s `DenyMachineCredentials` group — an API key
or OAuth client may not touch how its owner authenticates — and **outside**
`RequireMFA`, for the same reason TOTP enrolment is outside it: somebody who
has been told to enrol must be able to finish enrolling.

**Finishing registration satisfies this login's MFA challenge, the same way
finishing TOTP enrolment already does.** `POST /me/passkeys/register/finish`
flips `MFAPassed` on success, mirroring `handleMFAEnrollConfirm`. Without
this a session admitted through the first-enrolment branch above — no factor
at all yet — could register a passkey and still be refused by `RequireMFA`
until it separately ran the sign-in ceremony against the key it had just
proved it held. Unconditional, matching TOTP: for the other way past the
guard (an already-protected account's owner registering a replacement key,
already holding the flag), setting it again is a no-op. Found as item 3 of
[#307](https://github.com/PubliciaLLC/go-help-desk/issues/307).

**Changing factors needs a session that *proved* one, not one that owed
none.** `MFAPassed` is true both when a login proved a second factor and when
it owed none — MFA off, or optional for the account's role. The two routes
that add or replace a factor (`requireFactorOrFirstEnrolment`, and TOTP
enrolment's re-enrol check) read a separate session fact, `FactorVerified`,
which is set only by:

- a TOTP code (`/auth/local/mfa/verify`) or passkey sign-in;
- this session finishing a TOTP enrolment or passkey registration;
- an SSO sign-in whose identity provider asserted MFA — OIDC `amr` containing
  `mfa` (RFC 8176), or SAML `authnmethodsreferences` containing
  `http://schemas.microsoft.com/claims/multipleauthn`. Entra ID sends neither
  by default: add the `amr` optional claim to the app registration (for SAML,
  with `include_granular_amr`). Without it SSO sign-in still works, but a user
  who also has a local factor must enter it before changing factors.

Without this, a password-only session on an account where MFA was optional
could replace the owner's factor at any time after they enrolled — no race
needed ([#333](https://github.com/PubliciaLLC/go-help-desk/issues/333)).

**Adding a factor ends every other session,** the same way a password change
does: finishing TOTP enrolment or passkey registration revokes the account's
sessions and re-issues the current one. A session someone opened with the
password before the owner protected the account does not outlive that
protection. Enrolment confirm also re-runs the guard, rather than trusting the
check made when enrolment was staged
([#327](https://github.com/PubliciaLLC/go-help-desk/issues/327)).

**A first TOTP enrolment is a conditional write.** The guard asks "does this
account hold no factor yet?" and the confirm then writes, which are two
statements; two confirmations racing on a fresh account could both pass the
question and the later write won. A session that has not proved a factor now
writes with `WHERE NOT mfa_enabled`, and the loser is answered 403 like any
other attempt on a protected account. A session that has proved one (a
rotation) still overwrites, on purpose
([#338](https://github.com/PubliciaLLC/go-help-desk/issues/338)). Passkey
registration has no such condition in its write.

Sessions written before `FactorVerified` existed read it as false. A user with
a factor signs in again before changing factors; nothing else changes.

**Verifying from the Account page.** A session that owed no factor at login
(MFA off, optional for the role, an SSO provider that asserted nothing, a
session older than `FactorVerified`) is refused on the factor routes with 403
`mfa_required`, and the login page never asked it for anything. The Account
page therefore answers that refusal itself: it offers a code field when the
account has an authenticator app and a passkey button when it has passkeys,
posts to the same `/auth/local/mfa/verify` and `/auth/local/passkey/*` routes
the login page uses, and then retries what the person was doing. No new route
([#336](https://github.com/PubliciaLLC/go-help-desk/issues/336)). A wrong code
or a refused key answers 401 on a live session, so the client does not treat
`invalid_mfa_code` or `assertion_refused` as a lost session.

**Signing in.** `POST /auth/local/passkey/start` and
`POST /auth/local/passkey/finish` sit beside `/auth/local/mfa/verify` and do
what it does: on a valid assertion, re-issue the session with `MFAPassed` true.
The gate downstream is `Actor.MFAPassed`, which already exists and is already
enforced by `RequireMFA` everywhere it matters, so no route learns a new idea.

**`handleLocalLogin` has to grow a third answer, and this is the one place the
existing machinery does not simply absorb passkeys.** It currently computes two
independent booleans and returns them as `mfa_needed` and
`mfa_enrollment_needed`:

```go
mfaNeeded := mfaEnabled && u.MFAEnabled
mfaEnrollmentNeeded := mfaEnabled && !u.MFAEnabled && s.adminSvc.MFARequiredFor(...)
```

Both key off `u.MFAEnabled`, which is the TOTP column and not "this account has
a second factor". Trace an account with one registered passkey, no TOTP, and a
role that enforces MFA, signing in for the *second* time — after it has already
enrolled. `u.MFAEnabled` is still false, so the server answers "you still need
to enrol" and never "assert your passkey". There is no wire outcome for *has a
factor, must use it, and it is not TOTP*, so that account can never sign in
again.

This is a third credential state, not a flag that fails to clear, and the pair
of booleans cannot express it. Login answers with an explicit state — no factor
required, already satisfied, verify TOTP, verify passkey, or enrol — and the
login page learns the new one. It is part of the first release, because
"register a passkey" without "sign in with it" is not a feature.

**Satisfying the MFA requirement.** `MFARequiredFor(role)` is satisfied by a
TOTP enrolment **or** at least one registered passkey. Refusing the
phishing-resistant factor because it is not the older one would be perverse.
This means `mfa_enabled` and `mfa_enforced_roles` keep their current meanings
and an operator has nothing to reconfigure.

**`last_used_at` is written off the authentication path.** It answers "which of
these keys is still in use", which is what somebody wants to know before
removing one and cannot be reconstructed afterwards — but it is a timestamp
nothing enforces, and a sign-in should not wait on it or fail because of it.
It follows the shape already used for webhook dispatch in
`internal/server/notify/webhook.go`: handed to a goroutine with a context that
does not belong to the request, so cancelling the response cannot cancel the
write. Approximately right is right enough for this field.

**Losing a key.** Two ways, and only one of them is built.

Its owner removes it themselves, from their own account page, and registers a
new one. That is what `DELETE /me/passkeys/{id}` is for, and it is the ordinary
case: somebody replacing a phone still has the old one, or still has another
factor, and needs nobody's help.

An administrator removing ONE credential for somebody else is **not built yet**.
Every passkey route lives under `/me`; `adminRouter` has none. What an
administrator can do is the admin page's "Reset MFA", which clears **every**
second factor the account holds: the authenticator and all registered
passkeys, ending the account's sessions in the same statement. It used to
clear only the authenticator, so on a passkey-only account it reported success
and the person was still locked out by the key they had lost
([#307](https://github.com/PubliciaLLC/go-help-desk/issues/307), item 2).
`reset-factors` on the server, described below, does the same from the command
line, and is the answer for a *sole* administrator.

Both are one database statement, all or nothing (`ClearFactors`): a failure
part-way used to leave an account with no factor and its old sessions alive,
and now leaves it exactly as it was (#307, item 4).

An earlier draft of this paragraph said an administrator removes a credential
from the user's admin page, "the same control surface as Reset MFA", in the
present tense. That control surface does not exist. It is the right place for
it when it is built, and saying so as though it already were is how an
operator ends up looking for a button that was never written. Found by the
session-B review of [#302](https://github.com/PubliciaLLC/go-help-desk/pull/302).

**Self-recovery covers an account with *nothing* enrolled, and not a lost
key.** The distinction matters and an earlier draft of this section ran the two
together.

Enrolment lives outside `RequireMFA`, so somebody whose factors have been
cleared — by an administrator, or because they never had any — signs in with
their password, reaches enrolment and recovers alone. That is pinned by
`TestSoleAdministrator_CanSelfRecoverWithNoSecondFactor`.

An account that still *has* a registered factor is a different case, and both
enrolment doors refuse it: a session that has not passed MFA cannot add or
remove a factor on an account that already has one. That refusal is not
optional. Without it, somebody holding only the password registers their own
key or enrols their own authenticator and thereby obtains the second factor —
passing the gate rather than breaking it.

The consequence is a genuine lockout, and it should be stated rather than
discovered: **an administrator whose registered key is lost cannot recover
alone.** The recovery is `reset-factors` on the server, described below — not
an administrator clearing the credential for them, which is not built. For a
*sole* administrator there would be no other administrator to ask in any case,
and setup does not reopen.

(This paragraph said "another administrator removes the credential from their
admin page" until the correction in the "Losing a key" section above. Two
paragraphs of the same section then disagreed, which is worse than either
being wrong alone. Found by the pre-merge gate on #302.)

This is not new with passkeys. `GenerateMFASecret` has refused re-enrolment
for a TOTP-protected account since the re-enrolment fix, so a sole
administrator who loses their authenticator is in exactly the same position
today. Passkeys extend the same lockout to a second kind of factor rather than
creating it.

**The guard's fourth case still belongs with passwordless, and an earlier
draft of this section was wrong in both directions about why.**

It first said the case could wait because self-recovery covers a lost key. It
does not: an account that still has a registered factor is refused at both
enrolment doors, deliberately. Then it said the case was therefore reachable
today. That is also wrong, and checking what is actually a way *in* settles
it: the entry points are local login, SAML and OIDC. A second factor gates a
session that has already authenticated; it is not a way in by itself. So
removing somebody's last second factor cannot strand them while a first factor
exists, and there is no operation for a fourth case to refuse yet.

What does strand somebody today is losing a registered key, which
`reset-factors` above answers, and one thing that is not about second factors
at all: an account provisioned by an identity provider has no password, so
switching that provider off removes its only way in while leaving the row and
the administrator count untouched. Every existing guard passes. Tracked as
[#300](https://github.com/PubliciaLLC/go-help-desk/issues/300).

**The way back in is a command run on the server**, not a recovery code and
not a second factor required up front:

```
go-help-desk reset-factors <email>
```

It clears the account's TOTP enrolment and removes its registered passkeys, so
the next sign-in reaches enrolment and the person starts again. It is the
answer for every cause of lockout rather than only a lost key, and for the
sole administrator it is the only answer there can be, since the web path must
keep refusing — a password alone being enough to replace somebody's second
factor is the bypass the guards exist to prevent.

It grants nothing new. Anyone able to run it already has the filesystem and
the database credentials, which is to say they already have everything. That
is what makes it the right channel: it does not widen the web-facing surface
at all, which a recovery code — another secret at rest, worth stealing, and
the exact property passkeys exist to remove — would.

**It writes an audit entry**, naming the account and the operating system
user and host that ran the command, with no actor ID — nobody signed in to do
this, and inventing one would record a claim rather than a fact. The same
entry shape (`entity_type: "user"`, `action: "mfa_reset"`) is written by the
admin page's "Reset MFA", and by an administrator resetting somebody's
password (`action: "password_reset_by_admin"`), naming the administrator's
account as the actor. This does not prevent anything — whoever can run this
command already holds everything an audit entry could gate — but the
ordinary use of it is an administrator helping a colleague who lost a phone,
and that is a normal operational event that belongs in the trail alongside
every other account change. See [#306](https://github.com/PubliciaLLC/go-help-desk/issues/306).

**This command is a precondition for the guard's fourth case, not its
trigger.** "Refuse to remove the last way in" is only half an answer without
"and here is how you recover when it happens anyway"; building either alone
leaves an operator holding the wrong half. So the command comes first, and the
fourth case still arrives with passwordless, for the reason given further up:
until the password stops being a way in, removing a second factor strands
nobody, and there is no operation for the fourth case to refuse.

(An earlier draft of this line said the fourth case "ships with that command
and not before", which read as though it ships now and contradicted the
paragraph above. Found by the pre-merge gate on #302.)

**That test is a precondition on passwordless sign-in, not a formality.** When
the password stops being a way in, self-recovery stops working, and removing an
administrator's last credential becomes the same permanent mistake as deleting
the last administrator — setup does not reopen. The guard grows its fourth case
in the change that introduces passwordless, and the test above has to be made
to pass for passkeys before that change is considered done.

**`BASE_URL` becomes security-relevant.** A WebAuthn credential is bound to its
origin, so this setting stops being about building links in emails and becomes
part of whether authentication works at all. An instance that changes domain
invalidates every registered credential and every user re-registers. This is
stated wherever the variable is documented, not in a footnote.

**Not in the first release**, written down so it is not rediscovered as a gap:
passwordless sign-in; attestation verification against a metadata service;
a per-role "passkey required, TOTP no longer sufficient" policy; and any route
by which a machine credential could register or use a passkey —
`DenyMachineCredentials` refuses that today and will keep refusing it.

### SAML (Optional, Off by Default)

- Toggle in admin settings
- When enabled: all users (Admin, Staff, User) authenticate via SAML
- **Admin failsafe**: admins can still log in with local username/password when SAML is enabled
- Non-admin local auth is disabled when SAML is on

**Supported IdPs:**
- Okta
- Azure AD / Entra ID
- Google Workspace
- (Standard SAML 2.0 — additional IdPs should work via metadata import)

The SP root URL passed to the SAML library carries a trailing slash
(`{baseURL}/api/v1/auth/`) rather than the bare prefix — see
`auth.NewSAMLMiddleware`'s own comment. Without it, the library's relative
URL resolution computes `saml/metadata` and `saml/acs` one path segment
short of the routes this server actually registers, and every real request
to them 404s: no login could complete and no IdP could fetch this
instance's metadata, in any configuration. Found and fixed while testing
#304; pinned by `TestNewSAMLMiddleware_ComputesRoutesMatchingTheServerMounts`.

The SAML library's own login cookie (`token`, a signed JWT valid for an hour)
is a hand-over, not a session: `/auth/saml/complete` clears it as soon as it
has read it, whatever the outcome, and the app session it writes is the only
credential from then on. Left in place it would outlive every session
revocation (password change, MFA reset, a new factor) and let a browser that
held it mint a fresh session, with whatever MFA the original assertion claimed
(#337). Pinned by `TestSAMLComplete_SpendsTheLibraryCookie`.

"Spent" means the browser is told to delete the cookie, under the same name,
domain and path the library set it with. It is not server-side invalidation:
the JWT is stateless, so a copy captured before the hand-over stays valid
until it expires, an hour by default.

### Identity provider lockout guard (#300)

A federated account (SAML or OIDC) can have no local password at all —
`password_hash` empty, local login refuses it with 401. Its only way in is
that specific provider. Disabling or clearing that provider's configuration
in **Admin → Settings** removes the account's only channel while leaving the
row, the role and the active-administrator count completely untouched — the
same class of mistake the last-administrator guard (see User Management,
above) exists to prevent, reached through a door that guard does not watch,
since it watches the administrator ROW, not their ability to authenticate.

SAML reachability depends only on the three config fields being non-empty —
`reloadSAML` and `buildSAMLMiddleware` have always gated on that alone, and
still do. An early draft of this guard gave SAML an `enabled` flag mirroring
OIDC's, reusing the existing `saml_enabled` setting (the settings page's
"Enable SAML login" toggle has always written it). That setting is not
dead: `user.IsLocalAuthAllowed` reads it to decide whether non-admins keep
password login once SAML is configured — a stricter posture an operator
opts into separately from whether SAML itself is running. Wiring it into
whether the middleware loads at all would have conflated the two, and a
migration backfilling it to `true` for every already-configured instance
would have silently refused password login to every non-administrator on
any instance that had been running SAML and local login side by side.
Caught in review before merge and reverted; see PR #304's thread for the
full trace. SAML has no `enabled` concept in this guard, and does not need
one — see the incomplete-config paragraph below for why OIDC's is different.

Saving the OIDC or SAML configuration checks what each provider's
reachability will be immediately afterward and looks at every active
administrator:

- If the change would leave **every** active administrator with no way to
  authenticate, the save is refused (400) — the same severity as
  `ErrLastAdmin`, and for the same reason: this is the unrecoverable case.
- If it strands **some** administrators but at least one other can still
  sign in and fix things, the save is allowed and a warning names who is
  affected — refusing here would just move the unrecoverable-lockout shape
  onto somebody else's account instead of preventing it, and an operator
  migrating providers deliberately should not be blocked by a stranding they
  already know about.
- A password is always a viable channel, independent of either provider's
  state. A federated subject is only viable while its OWN provider is
  reachable — an OIDC subject is not a channel through SAML, and vice versa.
  MFA (TOTP, and passkeys where that lands) is deliberately not consulted: a
  second factor is never a way IN on its own, so it cannot rescue an
  otherwise-stranded administrator and cannot strand one either.

This reads the active-administrator list, decides, and only then writes the
setting — unlike the last-administrator guard's own statements, which decide
and write a single row atomically in one UPDATE. A narrow race against a
concurrent user-role change or a second settings save is accepted rather
than closed: this is a deliberate, infrequent action from the admin settings
page, not a path an unauthenticated attacker can drive.

"Reachability" is not the same check everywhere this guard runs, and that
difference is deliberate rather than an inconsistency to fix:

- The two dedicated endpoints (`PUT /admin/oidc`, `PUT /admin/saml`) build
  the real provider/middleware against the candidate configuration —
  running actual OIDC discovery, or actually fetching and parsing the SAML
  IdP's metadata — *before* persisting anything, and the guard's decision is
  that real outcome. An earlier version of this guard asked only "are the
  fields non-empty", which is wrong in the dangerous direction: a
  well-formed but unreachable IdP (a typo'd issuer URL, a metadata endpoint
  that is down) would have sailed through as "reachable" right up until the
  moment it actually mattered. The already-built object is what gets
  committed on success, rather than a second, possibly-different attempt —
  the guard's decision and the live effect must agree.
- The generic `PATCH /admin/settings` route reaches these same keys (nothing
  stops a human session from setting `oidc_enabled` or blanking a SAML field
  through it) but does not live-reload either provider today, so there is no
  real construction attempt for it to observe. It falls back to the
  field-completeness check instead — narrower than the dedicated endpoints'
  own guard, but still real coverage for a route that, before this fix, had
  none at all: it could set `oidc_enabled: false` or blank any SAML field
  with no refusal and no warning, regardless of who it stranded.

An enabled-but-incomplete OIDC configuration (a blank issuer URL, client ID
or client secret) is refused outright (400) rather than reachability-checked,
through either write path. `buildOIDCProvider`'s own fail-safe for that shape
used to report reachability as whatever the currently-live provider already
says — correct for the running process, which still has the old provider to
fall back on, but wrong for the row being persisted: at the next restart
there is no live provider left, so `InitOIDC` comes up with no OIDC at all. A
save that only looked safe because an old, unrelated config was still live at
the moment of saving is exactly the gap this guard exists to close, so it
isn't allowed to reach the guard in the first place. SAML has no equivalent
case for an incomplete config — `buildSAMLMiddleware` treats any blank field
as unreachable unconditionally, with no fail-safe carve-out and no `enabled`
flag to have one for in the first place.

A second, closely related review round found the same confusion one branch
over: a **complete** candidate configuration whose real construction attempt
genuinely fails (a typo'd issuer URL, an IdP that is briefly unreachable) was
*also* reported as reachable-or-not by asking the live process, rather than
by asking what the persisted row itself would do on a cold load — and this
half applies to both providers equally, not just OIDC. `buildOIDCProvider`
and `buildSAMLMiddleware` now answer these two different questions
separately: `commit` still drives the long-standing fail-safe for the LIVE
process (a bad edit or a transient outage leaves whatever is currently
running untouched, exactly as before), but the `reachable` value the guard
reasons about is unconditionally `false` whenever the candidate itself fails
to build — regardless of what a different, currently-live provider happens
to still be answering with at the moment of saving. Reasoning about the live
process and reasoning about the row being persisted are different questions,
and only one of them survives a restart.

The generic settings PATCH has its own version of the same principle at the
type level: `ssoSettingsWarning`'s merge of the request body over stored
values refuses outright (400) on any JSON type mismatch (`oidc_enabled` sent
as the string `"false"` rather than the boolean, say) rather than discarding
the `json.Unmarshal` error and reasoning about the old value — because the
`SetRaw` write immediately below persists the malformed value regardless, and
every real reader (`GetBool`/`GetString`, under `OIDCEnabled`, `GetSAMLConfig`,
`GetOIDCConfig`) fails that same unmarshal and silently returns the Go zero
value, flipping the actual setting to disabled/blank from that write onward.
A guard reasoning about one value while the real system reads a different one
from the identical bytes is worse than not reasoning at all, because it
reports confidence it does not have. The JSON literal `null` is the same
failure by a different mechanism, caught in a later review round:
`encoding/json`'s `Unmarshal` treats `null` into a non-pointer destination as
a silent no-op rather than an error, so a type-mismatch check alone still let
`{"oidc_enabled": null}` through unchanged — `unmarshalSetting` refuses `null`
explicitly, ahead of the type check, for every key this function reads.

Extending `reset-factors` to also set a password, so a locked-out federated
administrator has a complete way back rather than merely a warning that
would have stopped them getting here, is tracked separately (#300's option
4) and depends on `reset-factors` itself, which does not exist on this
branch.

### Guest Submission (Optional, Off by Default)

A visitor with no account files a ticket and is sent a per-ticket link. The link
is the whole credential, so it is treated like one: stored as a hash, replaced
whenever the ticket changes in a way the guest is told about, **read-only once
the ticket closes**, and expiring after thirty days if nothing happens at all.

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

- Toggle in admin settings
- Unauthenticated users submit a ticket at `/submit` and receive a **tracking number**
- Guest ticket form collects: **name** (required), **email** (required), **phone** (optional), subject, description, and category (active only — no type or item)
- The tracking number can be referenced when following up with the help desk by phone or email
- No account creation required

### Small screens

Two jobs are supported on a phone: **a staff member triaging away from their
desk**, and **a reporter filing a request and following it**. Administration is
not. An operator configuring categories, editing roles or managing API keys is
at a desk, and the ten admin tables are built for one.

That boundary is stated rather than implied, because the alternative is a
product that appears to work on a phone until somebody reaches a page that
does not.

**What each job covers.** The staff path is the queue, a ticket, and the
actions taken on it: read, reply, reassign, change status, resolve. The
reporter path is the new-ticket form, their own list, and the thread they can
read and reply to. Both include signing in.

**Where the work actually is.** The unauthenticated pages — sign-in,
registration, first-run setup, email verification, guest submission, the guest
ticket view, and the tracking-number form — render outside the application
shell, as centred cards with their own maximum widths. They already work at
phone size; measured at 390 CSS pixels, each fits with nothing wider than the
screen. Nothing in this section changes them.

Everything inside the shell does not work, and for one reason: the sidebar is
a fixed 240 pixels with no breakpoint, which is more than half of a 390-pixel
viewport. The pages beneath it then inherit a column too narrow to lay
anything out in. So the shell is the first change and the largest single
improvement; the queue and the ticket page follow it.

**The rule for the pages that are in scope.** No horizontal scrolling at 390
pixels, controls large enough to hit with a thumb, and a layout that stacks
rather than shrinks — a five-column table squeezed into a phone is not a
mobile layout, it is the same table with less room. Where a table carries one
row per thing, that becomes one card per thing.

**Not a claim of feature parity.** Everything a staff member can do to a ticket
from a desk they can do from a phone, because those actions live on the ticket
page. Bulk selection across a queue is the exception and stays desktop-only:
it is a multi-select over a table, which is the shape that does not translate.

Tracked as [#296](https://github.com/PubliciaLLC/go-help-desk/issues/296).

### Ticket Submission by Role

| Field | Guest | User (logged in) | Staff / Admin |
|-------|-------|-----------------|---------------|
| Name, email, phone | Required / optional | — (uses account) | — |
| Category | Active only | Active only | All |
| Type | — (not shown) | Active only | All |
| Item | — | — (not shown) | All |
| Priority | — (defaults to Medium) | — (defaults to Medium) | Selectable |
| Attachments | — | Yes | Yes |

Attachment upload is available to every authenticated non-guest user on the
API, and the reply composer offers the same control to every role that can
already upload at ticket creation (#175) — staff-only controls in that
composer are the internal-note flag, the customer-notify flag and canned
responses, not the attachment control. A guest cannot attach at ticket
creation (there is no ticket yet to attach to), but can attach to an
existing one afterward at `POST /api/v1/guest/attachments`, authorized the
same way `POST /api/v1/guest/replies` is: the ticket comes from the guest
token in the `Authorization` header, never from an id in the path, so there
is nothing for a guest to change to reach a different ticket. Which types are accepted is an operator setting, `attachment_allowed_types` — a JSON array of lowercase extensions with the leading dot, each matching `^\.[a-z0-9]{1,16}$`. The shipped default is PDF, DOCX, XLSX, TXT, LOG, JPG, JPEG, PNG, BMP; an empty array means this instance takes no attachments at all. `.jpg` and `.jpeg` name one format, so allowing either allows both. Changing it needs a signed-in administrator — an API key cannot widen what the instance accepts. Max 25 MB per file. Images (JPEG, PNG, BMP) are re-encoded to whichever of JPEG (quality 85) or PNG produces a smaller file — except an image with transparency in it, which is always PNG, because JPEG has no alpha channel and "smaller" would be comparing two different pictures. File names on disk are obfuscated (UUID-based); the original file name is preserved in the database for download.

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
`CLAMAV_ADDR`. The backend validation was already there; only the control was
missing.

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

**Attachments are download-only. There is no previewer, and there will not be
one.**

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

The second rule is decided by asking the detector, not by a list of our own,
and the difference is not cosmetic. Three earlier versions were each right for
the cases in front of them and wrong for the family they were generalised to.
A list of acceptable detected *extensions* called a container log
(`application/x-ndjson`), a config file (`text/xml`) and an exported contact
(`text/vcard`) files lying about themselves — a Kubernetes log arriving on a
ticket renamed and wrapped is exactly the failure this control exists to
avoid. A list of media types — `text/*` plus JSON and NDJSON — flagged a
GeoJSON document, which the registry files under its own name. Accepting
anything whose name ends `+json` or `+xml` admitted the Visio drawing above,
which is an archive. The tree has no such gap, because it is the same source
that produced the media type being judged: a rule written from the detector's
own answers cannot disagree with the detector.
A warning that fires on ordinary files is one staff learn to click past, which
is worse than no warning at all — so where a legitimate case fires, the fix is
to widen one of these two rules and never to soften the flag.

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

**What an upgrade actually changes.** It is not parity with 1.2.0, and this
document used to claim it was. 1.2.0 checked a hard-coded signature against the
claimed extension and had no entry for `.txt` or `.log`, so it was strict about
a handful of names and blind to the rest; this release detects the content and
judges it against the operator's allowlist. That moves rows in both directions.
Measured through the upload handler on a default instance:

| Upload | 1.2.0 | Now |
|---|---|---|
| plain text named `.pdf`, `.docx` or `.xlsx` | `415` | `201`, stored under its own name, flagged |
| a real PNG named `.jpg`, or a real PNG named `.pdf` | `415` | `201` |
| a plain ZIP named `.docx` | `201` | `415` |
| under four bytes, text-looking, under a text or document name | `415` | `201` |
| under four bytes, unplaceable, under a binary name | `415` | `415` |
| under four bytes, text-looking, under an image name | `415` | `422` |
| under four bytes, unplaceable, under an image name | `415` | `415` |
| text named `.png` or `.jpg` | `415` | `422` `invalid_image` |
| a `.txt` or `.log` of four bytes or more, whatever is inside it | `201` | `201` |
| HTML named `.pdf` | `415` | `415` |

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

The first row is the one to understand rather than to fix. A `.pdf` holding
plain text is a contradiction and is flagged, and it is neither wrapped nor
refused, because the judgement for tier 2 is the operator's own allowlist and
`.txt` is on it: the content is something this instance would have accepted
under its own name, so there is nothing to contain — only something to say.
Refusing it would need a second list of "types that may not hide inside other
types", which is the second list this design exists to avoid keeping correct.
1.2.0 caught that one case by looking for `%PDF`, and the same rule let HTML
into a `.txt` completely unexamined — the two rows are the same trade seen from
either end.

The third row is the stricter direction and is deliberate: 1.2.0 accepted any
ZIP under a `.docx` name because both start `PK\x03\x04`, and an Office
document is now identified as an Office document.

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
  allowance on a page refresh. There is no queue, because this project has no
  background job runner (#126) and a queue would be a table nothing drains.
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

**Notable public endpoints (no auth):**
- `GET /api/v1/site` — branding info and app version; used by the SPA shell before authentication
- `GET /api/v1/setup/status` — whether first-run setup is needed

### MCP Interface

Exposes help desk operations as an MCP server for AI tool integration, served
over SSE at `/mcp/`.

**Tools**

| Tool | Who may call it | Notes |
|------|-----------------|-------|
| `get_ticket` | any signed-in user | By UUID or tracking number. Returns the reply thread and linked tickets; internal notes are omitted for reporting users. |
| `list_tickets` | any signed-in user | Optional `assignee_user_id`, `status_id`, `priority`, `category_id`, `q` filters. `limit` defaults to 20 and is clamped to 100; `offset` pages the full result set. |
| `list_categories` | any signed-in user | The Category/Type/Item tree, nested, for filling in CTI. |
| `list_statuses` | any signed-in user | The statuses this instance defines. |
| `create_ticket` | staff, admin | Category required; Type and Item optional. `reporter_user_id` names the subject of the ticket and defaults to the caller. |
| `add_reply` | staff, admin | `internal: true` posts a staff-only note and does not notify the reporter. |
| `assign_ticket` | staff, admin | To a user or a group. |
| `update_ticket_status` | staff, admin | Target status must be one the caller's role may transition to. A ticket in Closed cannot be moved out of it unless `closed_reopen_policy` lets the caller's role (#349; off by default): the result says reopening is disabled or restricted and points at `create_follow_up`. There is no separate reopen tool and never was; this is MCP's path out of Closed, through the same service rule as REST. |
| `create_follow_up` | staff, admin | `ticket_id` of a **Closed** ticket. Opens a new, linked ticket (see Closed is terminal); refused for a ticket that is not closed. |

**Authorization.** `/mcp/` runs behind the same middleware chain as `/api/`, so
every call is authenticated. Beyond that, the transport does not decide what a
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

### Authentication Methods

| Consumer | Auth Method | Details |
|----------|------------|---------|
| Browser (SPA) | Session cookies | HttpOnly cookie carrying an opaque id; the session itself is a row in Postgres. Backed by local auth, SAML or OIDC. |
| Formal integrations (JIRA, chatbots, CI) | OAuth2 client credentials | client_id + client_secret → short-lived JWT, scoped per integration |
| Lightweight scripting / webhooks | API keys | Hashed bearer tokens with scoped permissions |
| MCP | Inherits from above | Sits on top of the REST API. Same authentication, and scopes are enforced per tool. |

Sessions are server-side rows, not self-contained cookies, because that is the
only shape in which a session can be revoked. Logout, a password change, an MFA
reset, a role change, disabling and deleting all take effect on the next
request. A session whose owner is disabled or deleted stops loading whether or
not anything deleted it, and the session id rotates on login and on any
privilege change. Lifetime is 7 days.

### Credential Scopes

Machine credentials — API keys and OAuth clients — carry scopes. Browser
sessions do not: a session **is** the user, with whatever their role allows.
Scopes exist to give a machine credential *less* than its owner, and there is
nothing to narrow when a person is driving.

A scope is `resource:action`, where action is `read` or `write`.

| Resource | Covers |
|----------|--------|
| `tickets` | Tickets and everything under `/tickets/{id}` — replies, links, tags, attachments, custom fields, status transitions |
| `users` | User administration |
| `groups` | Groups, their members, and their category/type scopes |
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
   An API key acts at its owner's role; an OAuth client acts as staff.
2. **An empty scope list denies everything.** A credential with no scopes
   reaches nothing at all.
3. **Write implies read** on the same resource. An integration that may create
   tickets but not read them back is not a useful shape.
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
authenticates: its own (`/me/password`, `/me/mfa/enroll*`), and — through the
admin surface — **any administrator's**. It may not create an administrator,
promote a user to administrator, or reset an administrator's password or MFA.

The rule is about administrators rather than about the credential's own owner
because refusing only the owner closed the path and not the outcome: every key
reaching `/admin` is administrator-owned, so `users:write` allowed creating a
second administrator, signing in as them, and resetting the first one's
password. Guarding only promotion left the reverse open too — demote an
administrator, reset the now-staff account's password, sign in — so the rule
covers the whole account in either direction. Managing non-administrator users
stays available.

Three more things are off limits to a machine credential, for the same reason:

- **Changing the SAML or OIDC configuration.** Login providers are settable from
  the admin UI and nowhere else. Repointing the identity provider at one the
  caller controls, then asserting a federated administrator's subject, yields an
  administrator session — and it is also the last way a credential could make
  the server fetch a URL of the caller's choosing on the internal network.
  Blocked on both doors: the dedicated config routes and the `saml_*` / `oidc_*`
  settings keys. Reading the configuration stays available to automation, since
  the handlers already blank the secrets.
- **Changing an auth-critical setting** — MFA enablement and enforcement, the
  SAML/OIDC keys, the email-domain allowlist, the signup toggles, and guest
  submission (#177: it decides not just whether anonymous people can file a
  ticket, but also, since the category catalogue stopped being anonymous,
  whether that catalogue is readable without a session at all — one flag,
  two exposures). Ordinary configuration such as the site name stays
  automatable.
- **Verifying an MFA code** (`POST /auth/local/mfa/verify`). A machine
  credential reaching it could spend the account's durable failed-attempt budget
  and lock the owner out repeatedly.
- **Issuing a credential broader than itself.** Otherwise `credentials:write` is
  every scope: hold only that, mint a key with `users:write`, use it. A
  signed-in administrator is exempt — they already hold everything a credential
  could be granted, so they are not escalating.

Scopes cannot express any of this. The narrowest possible key still belongs to
its owner, so any scope reaching those routes reaches account takeover.

`GET /api/v1/admin/scopes` returns the catalogue. The admin UI builds its
picker from it so the two cannot drift.

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
  compressed formats expand, and a 169 KB PNG decodes to 142 MB.

  Two bounds, and the second took five rounds of review to get right. Twenty-
  five megapixels, and 100 MB of decoder allocation — which is not the same
  number as the picture's size, because the JPEG decoder allocates far more
  than the picture it produces. A progressive JPEG holds every DCT coefficient
  until the image is reconstructed; a CMYK or RGB one decodes through a second
  full-resolution image. Counting pixels and assuming four bytes each let a
  214 KB file cost 403 MB, and each narrower rule that replaced it let a
  differently-shaped file through: 16-bit, then progressive, then CMYK, then
  RGB, then an Adobe marker moved after the scan data.

  So the rule is no longer "know every shape". A JPEG whose header cannot be
  read is **refused**, rather than falling back to a weaker estimate. Every
  real JPEG parses; one that does not is one somebody built not to, and "I
  cannot tell how much this will cost" is a reason to refuse. That converts
  the next gap in the estimate from a way through into a refusal, which is
  worth more than any single thing the estimate knows. A limit on how many
  images are decoded at once bounds the process rather than the request.
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
- **Webhook payloads omit the body of an internal note** and carry an
  `internal` flag, so a subscriber can tell a staff-only note from a public
  reply. Before, it received the text of every internal note and could not tell
  them apart.
- **Chat/ITSM payload formats escape mentions and never carry an internal
  note body.** Slack escapes `&`, `<` and `>` in user-chosen text, so a
  subject of `<!channel>` cannot page a channel; Discord always sends
  `allowed_mentions: {"parse": []}`, so `@everyone`-style text cannot ping a
  server. All four formats (Slack, Teams, Discord, JIRA) are built from the
  same summary the raw format's internal-note omission already produces, so
  an internal note's body reaches none of them, not just the raw payload.

**Scopes were documented here before they were enforced.** Until 1.2.0 they were
accepted, stored and returned by the API, and no code read them — every
credential issued as restricted was unrestricted. Enforcement in 1.2.0 is a
breaking change: credentials created before it carry no scopes and are therefore
denied, and must be re-issued.

---

## Plugin Infrastructure (v2)

Deferred from v1. The data model and an admin CRUD shell exist
(`internal/domain/plugin/`, `handler_admin_plugins.go`) — a plugin record can be
listed, enabled and disabled — but none of the capabilities below are wired up:
install and uninstall return `501 not_implemented`, no WASM runtime is linked
in, and `plugin.Registry.Dispatch` is never called from the ticket lifecycle,
so an enabled plugin still receives no events. Treat this section as the
target design for v2, not a description of what 1.3 ships.

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
- **Webhooks** — configurable HTTP callbacks for ticket lifecycle events. These
  do carry the full event payload, subject and reply body included: a webhook
  target is registered by an administrator, not chosen by a reporter.
  Administrators manage them under **Admin → Webhooks** (URL, payload format,
  events, enable/disable, edit, delete). A secret is write-only: it signs
  deliveries and is never returned by the API or shown again, and leaving the
  field empty on edit keeps the stored one. There is no delivery log yet, so a
  failing hook is visible only in the server log.
- **Delivery is queued, not done on the request**
  ([#164](https://github.com/PubliciaLLC/go-help-desk/issues/164)). A request
  that triggers a notification writes it to `notification_outbox`, one row per
  channel (email, webhook), and returns. A worker in every server process
  claims due rows (`FOR UPDATE SKIP LOCKED`, so replicas never share one, with
  a ten-minute lease that returns a row whose worker died; a claim takes at
  most as many rows as can each run to their full time inside the
  lease — a minute for the database work, plus the 40 seconds the email
  channel's own SMTP limits allow — so rows are not reclaimed while still
  waiting their turn), sends, and deletes
  on success. A failed send is retried after 30 seconds, doubling to at most an
  hour, eight attempts in all; then the row is marked failed, logged, and
  deleted after thirty days. One row per channel means a failing channel is
  retried alone and the other is not sent twice; a channel that cannot carry an
  event type (webhooks never receive `guest.link_resent`) gets no row. A send
  that panics fails its row. Delivery is at least once: a
  worker that dies between sending and settling sends again after the lease.
  Webhooks are not retried on HTTP failure: their dispatcher already posts in
  the background and reports nothing back, unchanged by this.
- **Chat/ITSM payload formats (Slack, Teams, Discord, JIRA)** — v1, targeted for
  1.3. Not a plugin, and not a separate integration surface: a webhook
  subscription gains a `payload_format` setting (`raw` — today's behavior —
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

### Values

Stored normalized in `ticket_custom_field_values` (one row per ticket + field def, `value TEXT`) for filterability — not as a JSON blob. Staff can edit field values at any time after ticket creation from the ticket detail page.

Guests are shown no custom fields at all. The guest endpoint accepts none — a deliberate choice, since what an anonymous visitor may write into an operator's own fields is the operator's decision — and the public form no longer offers them. It did offer them for a while and threw the answers away on submit, which also meant a field marked required could stop a visitor filing a ticket at all. Regular authenticated users see category + type fields. Staff/admin see all levels.

---

## CTI-Linked Group Management

Admins can manage which groups handle each CTI node directly from the CTI editor (**Admin → Categories**), without navigating to the Groups page.

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
against it. The API refuses with 409, naming how many tickets depend on
it. A policy that has never matched a ticket can be deleted freely.

### Policy Matching

When a ticket is created, the system selects an SLA policy by specificity:

1. Priority + Category — most specific
2. Priority only
3. Category only
4. A catch-all (no Priority, no Category)
5. No SLA — if no policy matches

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
  status is called. Deactivating it does not stop it pausing: the
  ticket-detail status picker still lists inactive statuses and the
  transition is accepted. Deleting it removes the only way to pause. Whatever
  status is later named `Pending`, renamed back or newly created, becomes the
  pause status.
- Each time a ticket enters Pending, that timestamp is recorded as the start
  of a paused interval; each time it leaves Pending (to any other status), the
  interval closes and its length is added to the ticket's accumulated
  `tickets.sla_paused_seconds`.
- **Elapsed-toward-target**, at any instant, is `now - created_at -
  sla_paused_seconds`, with one adjustment: if the ticket is *currently*
  Pending, the still-open interval's length (from its start to now) is added
  on top, so a ticket does not appear to be making progress toward breach
  while it is actively paused.
- A response or resolution that lands while paused stops that clock the same
  way a status change would: `sla_records.first_response_at` / `resolved_at`
  is a timestamp, not a running total, so once it's set the elapsed-time
  formula above is irrelevant to it — only breaches still open at that
  moment continue to accrue pause time.
- Re-entering Pending after a reopen (see Ticket Lifecycle) resumes
  accumulating against the same policy and the same accumulated pause
  duration; a reopened ticket is not treated as a new SLA clock.

### Breach Evaluation

Because elapsed-toward-target keeps moving without any action on the ticket, a
breach can occur while nobody touches it — so, like ticket auto-close, this
cannot be computed only on read. A periodic background sweep evaluates every
open ticket with an attached policy and no existing breach timestamp for a
target still outstanding, and stamps `response_breached_at` /
`resolution_breached_at` the first time elapsed-toward-target exceeds the
policy's minutes for that target. The stamp, once set, does not clear itself
if a ticket is later reopened or its policy changes — a breach that happened
is a fact about what happened, not a live status.

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
