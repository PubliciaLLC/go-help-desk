# Go Help Desk — Design Document

## Overview

Open-source, self-hosted help desk system inspired by HESK, with SAML authentication, a plugin infrastructure, and a REST API. Built with a long-term roadmap toward SaaS (v4).

## Versioning Roadmap

| Version | Scope |
|---------|-------|
| **v1** | Core ticketing (with linked tickets, optional SLA tracking), local + SAML auth + MFA, plugin system (admin UI install), REST API, MCP interface, email + webhook notifications, Docker deployment |
| **v2** | Custom fields, CTI-linked group management, canned responses, full-text search (Postgres FTS) |
| **v3** | Reporting, knowledge base, custom admin-defined roles |
| **v4** | Multi-tenancy / SaaS, plugin registry, ITSM ticket types (Incident/SR/Problem/Change), Impact × Urgency priority matrix, default ticket type per CTI |

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
| **User** | Create tickets. View their own tickets. Update their own tickets unless status is Resolved. Reopen a Resolved ticket within a configurable window (admin setting: "Users can reopen tickets for X days after resolution"). |

**Custom admin-defined roles (v3):** admins will be able to define additional roles and grant a curated set of permissions (e.g., "Tier 1 Agent" with ticket read/reply but no assignment rights). The three built-in roles remain as defaults and cannot be removed. Permissions are stored as discrete capability flags rather than hardcoded in code, with the built-in roles expressed as preset bundles so existing behavior is preserved.

### User Management (Admin)

Admins manage accounts from **Admin → Users**. The user list is clickable — clicking a user opens a detail page with:

- **Profile** — edit display name, email address, and role. Changes take effect immediately.
- **Account info** — member since date, login type (Local / SSO / Local + SSO), MFA enrollment status.
- **MFA reset** — clears the TOTP secret so the user re-enrolls on next login. Only shown when the user has MFA enrolled.
- **Enable / Disable** — disabled accounts cannot log in. Tickets and history are preserved. Re-enable at any time.
- **Password reset** — set a new password directly (shown only for accounts with a local password). No email link required for admin-initiated resets.
- **Groups** — view current group membership, add to groups, or remove from groups.
- **Delete** — permanently removes the account. Tickets and replies the user created are preserved with a "removed user" attribution. Requires a second confirmation click. Prefer disabling instead when there is any chance the account may be needed again.

### Ticket Lifecycle

```
New → In Progress → Pending (waiting on user/vendor) → Resolved → [reopen window] → Closed
                                                           ↑            |
                                                           └── Reopened ┘ (within window)
```

- **Resolved**: ticket is answered/fixed. Starts the configurable reopen window.
- **Reopen window**: admin setting — "Users can reopen tickets for X days after resolution." Users can add a reply to reopen during this window. Set to 0 to disable user-initiated reopening entirely.
- **Reopen target status**: the status a ticket is moved to when it is reopened. Configured from **Admin → Settings → General → Ticket lifecycle → Reopen target status** (a picker limited to active, non-system statuses). Defaults to the first active custom status when unset.
- **Closed**: automatic transition after the reopen window expires. No further user updates. Staff/admin can still reopen manually.
- Statuses are customizable — admins can add intermediate statuses, but Resolved and Closed are system statuses with special behavior.
- Custom statuses can be **deactivated** (hidden from new-ticket flows) and **reactivated**. They can only be **deleted** when zero tickets are in that status. System statuses can never be deactivated or deleted.
- Every status transition is recorded in a **status history** timeline and displayed on the ticket detail page interleaved with replies, in chronological order. Events include: the old and new status names (with colors), who made the change (user display name or "System" for auto-close), and the timestamp. The initial status assignment at ticket creation is also recorded.

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
- Results appear after 2 characters are entered. Fetching is shown inline with a spinner.
- **Staff and admin** can submit the form to perform a direct **tracking number / UUID jump** — navigates immediately to the ticket if found, or shows an inline error.
- Users only see results from their own tickets; staff/admin see results from tickets assigned to them and their groups.
- Reply bodies are not indexed in v2 — only ticket subject and description. Deferred: searching reply content, fuzzy/typo-tolerant matching, and per-user saved searches.
- Uses Postgres's `english` text search configuration, which drops common English stop words (e.g. searching just "IT" matches nothing) — an accepted tradeoff of FTS, not a bug.

### Linked Tickets

Tickets can be linked to any other ticket regardless of status (including Closed). Link types:

- **Related to** — informational association
- **Parent / Child** — hierarchical grouping (e.g. a Problem with multiple Incidents)
- **Caused by** — causal relationship
- **Duplicate of** — marks a ticket as a duplicate (optionally auto-resolves the duplicate)

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
- **Logo** — uploaded via **Admin → Settings → Branding**. Accepted formats: PNG, JPEG, GIF, SVG. Max 2 MB. Raster images are proportionally scaled to fit within **320 × 64 px** and re-encoded as PNG; SVGs are validated as well-formed XML and scanned for disallowed content (scripts, event handlers, `javascript:` URIs). When set, the logo replaces the site name text in the sidebar.
- Both settings are stored in the database and managed via **Admin → Settings → Branding**.
- A public `GET /api/v1/site` endpoint returns `{name, logo_url, version}` — no authentication required, so the shell renders correctly before login.
- A public `GET /api/v1/logo` endpoint serves the stored logo file with a 5-minute cache header. `logo_url` in the site response points here when a logo is uploaded.

---

## Authentication

### Local Auth (Default)

- Username/password with bcrypt hashing
- Available for all roles by default
- **MFA** (optional toggle in admin settings): TOTP-based (Google Authenticator, Authy, etc.). When enabled, users enroll via QR code on next login. Admin can enforce MFA for specific roles or all users.

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

### Guest Submission (Optional, Off by Default)

A visitor with no account files a ticket and is sent a per-ticket link. The link
is the whole credential, so it is treated like one: stored as a hash, replaced
whenever the ticket changes in a way the guest is told about, revoked when the
ticket closes, and expiring after thirty days if nothing happens at all.

Submission is a separate public route rather than a relaxation of the ticket
router. Every route under `/tickets/{id}` would otherwise have to re-derive
whether the caller is a guest, which is the shape of the authorisation bug fixed
in 1.2.0.

- Toggle in admin settings
- Unauthenticated users submit a ticket at `/submit` and receive a **tracking number**
- Guest ticket form collects: **name** (required), **email** (required), **phone** (optional), subject, description, and category (active only — no type or item)
- The tracking number can be referenced when following up with the help desk by phone or email
- No account creation required

### Ticket Submission by Role

| Field | Guest | User (logged in) | Staff / Admin |
|-------|-------|-----------------|---------------|
| Name, email, phone | Required / optional | — (uses account) | — |
| Category | Active only | Active only | All |
| Type | — (not shown) | Active only | All |
| Item | — | — (not shown) | All |
| Priority | — (defaults to Medium) | — (defaults to Medium) | Selectable |
| Attachments | — | Yes | Yes |

Attachment upload is available to all authenticated (non-guest) users. Accepted formats: PDF, DOCX, XLSX, TXT, LOG, JPEG, PNG, BMP. Max 25 MB per file. Images (JPEG, PNG, BMP) are re-encoded to whichever of JPEG (quality 85) or PNG produces a smaller file. File names on disk are obfuscated (UUID-based); the original file name is preserved in the database for download.

### Attachment scanning

The scanner is configured with `CLAMAV_ADDR`, which an administrator can
override under **Admin → Settings**; the environment value is what the instance
starts with. Docker Compose ships ClamAV by default.

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

The admin UI does not render that yet — it shows only the insecure-secrets
warning — so today this is visible to an administrator who asks the API. The UI
is the obvious follow-up and is not in this change.

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

The corollary is that the **ticket attachment** allowlist does not have to be a
sanitiser: types a browser will execute — HTML, JavaScript, XML, SVG — are
refused outright rather than cleaned.

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
  browser intends to render the response. The two headers above are obeyed for
  a navigation; they are ignored for a subresource, so `<img src>` or a CSS
  `url()` pointed at an attachment would render it regardless of what the
  server said. An allow list of `document`, `empty` and absent — a destination
  nobody has invented yet is refused, and clients that send no header at all
  (older browsers, anything on a command line) have no renderer to protect.

A frontend test also fails if the application starts asking for an attachment
to be rendered, but it reads source text and cannot follow a value between
files. It is there to catch a mistake at review time, not to be the control.

Two caveats, both tracked in #165:

- **Content is only checked for types with a recognisable signature.** A `.pdf`
  must begin `%PDF`, a `.png` must have the PNG header, and so on — but `.txt`
  and `.log` have no signature to check, so a `.txt` containing HTML is
  accepted. It is stored under a name that says what it is and downloaded as an
  opaque blob like everything else, so nothing on this origin renders it — but
  the file on disk is still HTML, and whoever opens it afterwards is opening
  HTML. Step 2 of #165 records what the file actually is alongside what it
  claims to be, so at least the difference is visible.
- **The logo is the exception**, and the only upload this application renders
  inline. It is served under its own sandboxing policy (`sandbox; script-src
  'none'`), and an SVG is parsed and *refused* if it contains scripts, event
  handlers or `javascript:` URIs — refused, not stripped. #165 removes SVG from
  that uploader anyway: pattern-matching for dangerous SVG is a game you have
  to keep winning, and the 1.2.0 advisory already contains one escape from it.

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
| `update_ticket_status` | staff, admin | Target status must be one the caller's role may transition to. |

**Authorization.** `/mcp/` runs behind the same middleware chain as `/api/`, so
every call is authenticated. Beyond that, the transport does not decide what a
caller may do: each tool gates its own writes, and every read is filtered
through the same visibility rule the REST API applies — staff scope when
enforcement is on, own-tickets-only for reporting users. A ticket the caller may
not see reports "not found" rather than "forbidden", so tracking numbers cannot
be probed.

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
`assign_ticket`, `update_ticket_status`) need `tickets:write`. The two reference
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
  SAML/OIDC keys, the email-domain allowlist, and the signup toggles. Ordinary
  configuration such as the site name stays automatable.
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
- **Uploaded images are capped at 25 megapixels**, checked from the header
  before any decode. A byte-size limit is not a memory limit: compressed formats
  expand, and a 169 KB PNG decodes to 142 MB.
- **Security headers** on every response: a content security policy, `nosniff`,
  `X-Frame-Options: DENY` and a referrer policy. The uploaded logo is served
  with a stricter, sandboxed policy so an SVG cannot execute whatever it
  contains.
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

**Scopes were documented here before they were enforced.** Until 1.2.0 they were
accepted, stored and returned by the API, and no code read them — every
credential issued as restricted was unrestricted. Enforcement in 1.2.0 is a
breaking change: credentials created before it carry no scopes and are therefore
denied, and must be re-issued.

---

## Plugin Infrastructure

### Capabilities (v1)

- React to **ticket lifecycle events** (created, assigned, status changed, resolved, etc.)
- Add **custom fields / UI panels** to tickets
- Integrate **external systems** (Slack, Teams, Discord, JIRA, etc.)

### Theming

- **CSS/branding only** — logo, colors, fonts configurable in admin UI
- Not full layout-level theming

### Trust Model

- Both **1st-party and 3rd-party** plugins supported
- 3rd-party plugins run sandboxed with a restricted API surface

### Distribution

| Version | Method |
|---------|--------|
| v1 | Install/manage via **admin UI** (upload or URL) |
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
- Additional channels (Slack, Teams, Discord) are plugin territory

---

## Custom Fields (v2)

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

Guests see and can fill only category-level fields with `visible_on_new = true`. Regular authenticated users see category + type fields. Staff/admin see all levels.

---

## CTI-Linked Group Management (v2)

Admins can manage which groups handle each CTI node directly from the CTI editor (**Admin → Categories**), without navigating to the Groups page.

- Each expanded **Category** row shows a **Groups** subsection listing groups assigned at the category level (type_id = NULL in `group_scopes`).
- Each expanded **Type** row shows a **Groups** subsection listing groups assigned to that specific category + type pair.
- **Items do not have a group management section** — items do not factor into scope per the scope model.
- The "Add group" dropdown shows active groups not already assigned at that node.
- Adding or removing a group here is equivalent to using the Groups page; both update the same `group_scopes` table.

---

## Canned Responses (v2)

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

SLA tracking is an optional feature toggle available in **Admin → Settings → Features → SLA tracking**. It can also be pre-enabled at startup via the `SLA_ENABLED=true` environment variable.

### SLA Policies

When the SLA toggle is enabled, a **SLA Policies** management blade appears directly in the Features settings tab. Policies define the maximum time from ticket creation until a first response and until resolution. Each policy has:

| Field | Description |
|-------|-------------|
| **Name** | Display label, e.g. "Critical — 1h response" |
| **Priority** | Optional. `critical`, `high`, `medium`, or `low` — restricts the policy to tickets of that priority. Leave blank for "Any priority". |
| **Category** | Optional. Restricts the policy to a specific category. Leave blank for "All categories". |
| **Response target** | Minutes from ticket creation to first staff reply |
| **Resolution target** | Minutes from ticket creation to ticket resolved |

### Policy Matching

When a ticket is created, the system selects an SLA policy by specificity:

1. Priority + Category — most specific
2. Priority only
3. Category only
4. A catch-all (no Priority, no Category)
5. No SLA — if no policy matches

### SLA Indicators

The ticket queue shows a color-coded SLA indicator per ticket:

- **Green** — within SLA
- **Amber** — within 20% of the deadline
- **Red** — SLA breached

SLA timers are paused while a ticket is in a "Pending" status (waiting on the user) and resume when the ticket moves to any other status.
