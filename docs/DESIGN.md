# Open Help Desk — Design Document

## Overview

Open-source, self-hosted help desk system inspired by HESK, with SAML authentication, a plugin infrastructure, and a REST API. Built with a long-term roadmap toward SaaS (v4).

## Versioning Roadmap

| Version | Scope |
|---------|-------|
| **v1** | Core ticketing (with linked tickets, optional SLA tracking), local + SAML auth + MFA, plugin system (admin UI install), REST API, MCP interface, email + webhook notifications, Docker deployment |
| **v2** | Custom fields, canned responses |
| **v3** | Knowledge base, full-text search (Postgres FTS) |
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
- **Tracking number** (for guest access)
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
- **Reopen window**: admin setting — "Users can reopen tickets for X days after resolution." Users can add a reply to reopen during this window.
- **Closed**: automatic transition after the reopen window expires. No further user updates. Staff/admin can still reopen manually.
- Statuses are customizable — admins can add intermediate statuses, but Resolved and Closed are system statuses with special behavior.

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

- Searches **tracking number** (prefix match — e.g. `OHD-2025-0` matches all tickets in that series), **subject**, and **description** (substring match).
- Results appear after 2 characters are entered. Fetching is shown inline with a spinner.
- **Staff and admin** can submit the form to perform a direct **tracking number / UUID jump** — navigates immediately to the ticket if found, or shows an inline error.
- Users only see results from their own tickets; staff/admin see results from tickets assigned to them and their groups.
- Full-text search (Postgres FTS with ranking) is deferred to v3.

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

- **Site name** — the product name shown in the sidebar header and browser title. Defaults to "Open Help Desk".
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

- Toggle in admin settings
- Unauthenticated users can submit a ticket at `/submit` and receive a **tracking number**
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

Attachment upload is available to all authenticated (non-guest) users. Accepted formats: PDF, DOCX, XLSX, TXT, LOG, JPEG, PNG, BMP. Max 25 MB per file. Images (JPEG, PNG, BMP) are re-encoded to whichever of JPEG (quality 85) or PNG produces a smaller file. File names on disk are obfuscated (UUID-based); the original file name is preserved in the database for download. Optional ClamAV virus scanning is configurable via the `CLAMAV_ADDR` environment variable — if the scanner is unavailable or unconfigured, uploads proceed normally with a logged warning.

---

## API

### REST API

Serves both the frontend SPA and external integrations.

**Notable public endpoints (no auth):**
- `GET /api/v1/site` — branding info and app version; used by the SPA shell before authentication
- `GET /api/v1/setup/status` — whether first-run setup is needed

### MCP Interface

Exposes help desk operations as an MCP server for AI tool integration.

### Authentication Methods

| Consumer | Auth Method | Details |
|----------|------------|---------|
| Browser (SPA) | Session cookies | HttpOnly cookies backed by SAML or local auth |
| Formal integrations (JIRA, chatbots, CI) | OAuth2 client credentials | client_id + client_secret → short-lived JWT, scoped per integration |
| Lightweight scripting / webhooks | API keys | Hashed bearer tokens with scoped permissions |
| MCP | Inherits from above | Sits on top of REST API, same auth applies |

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

- **Email** — ticket creation, assignment, status changes, replies
- **Webhooks** — configurable HTTP callbacks for ticket lifecycle events
- Additional channels (Slack, Teams, Discord) are plugin territory
