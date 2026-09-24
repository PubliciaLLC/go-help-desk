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

Attachment upload is available to all authenticated (non-guest) users. Which types are accepted is an operator setting, `attachment_allowed_types` — a JSON array of lowercase extensions with the leading dot, each matching `^\.[a-z0-9]{1,16}$`. The shipped default is PDF, DOCX, XLSX, TXT, LOG, JPG, JPEG, PNG, BMP; an empty array means this instance takes no attachments at all. `.jpg` and `.jpeg` name one format, so allowing either allows both. Changing it needs a signed-in administrator — an API key cannot widen what the instance accepts. Max 25 MB per file. Images (JPEG, PNG, BMP) are re-encoded to whichever of JPEG (quality 85) or PNG produces a smaller file. File names on disk are obfuscated (UUID-based); the original file name is preserved in the database for download.

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
  is inert text: anything `text/*`, plus JSON and NDJSON, which are text that
  the IANA registry happens to file under `application/`. **`text/html` is
  included**, and used not to be. The exclusion was argued from "HTML runs when
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

The second rule is decided on the media type and not on a list of detected
extensions, and the difference is not cosmetic. Measured: a container log
detects as `application/x-ndjson`, a config file as `text/xml`, an exported
contact as `text/vcard`. A list of acceptable detected extensions called every
one of them a file lying about itself, and a Kubernetes log arriving on a
ticket renamed and wrapped is exactly the failure this control exists to avoid.
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
`.htm`, `.tif`, `.yml`, `.mpg` — and an operator who wants the lenient answer
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
*Check again* control where the state allows one. The line the summary was taken
from is marked, or the row's single sentence looks as though it came from
nowhere.

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

**`known` is the one verdict here that renders as reassurance**, and it is an
exception for a reason worth stating: a named feed made a positive claim about
the file. Everywhere else in this feature an absence must never read as safety
— `clean` only means engines ran and found nothing, `unseen` only means nobody
has submitted it — and none of those has anybody standing behind it.

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

Only PolySwarm can produce it today, from its `KNOWN_GOOD` state; the other two
providers never return it.

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
