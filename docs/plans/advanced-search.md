# Advanced ticket search — design proposal (1.3.0)

Status: **proposal, not approved.** Nothing in this document is to be built
until the owner approves it. Base: `origin/v1.3.0-beta` at `bc2f7a14`.

The request: search tickets "by requester, by date range, by ticket #, etc."
This proposal adds filters to `GET /api/v1/tickets`, the ticket list page and
MCP `list_tickets`, using one SQL query for all of them.

---

## 0. What exists today, and two things found while reading it

**Today.** `GET /tickets` takes `q`, `scope` (`mine`, `unassigned`, `all`,
`visible`), `reporter_id` (admin only), `assignee_group_id`, `limit` and
`offset`. The handler (`handleListTickets`, `internal/server/handler_tickets.go`)
picks **one** of these in order (`reporter_id`, then `assignee_group_id`, then
`scope`). The first one present wins and the others are ignored. Each branch
calls its own pair of queries (`List…`/`Search…`), which makes 12 queries in
`queries/tickets.sql`. Staff `mine` with scope enforcement off merges 1+N
queries in Go and sorts by `created_at`. That is why a search on that queue is
newest first and not ranked (the OpenAPI text says so).

"By ticket #" is already covered. `q` matches a tracking-number prefix
(`GHD-2026-0…`), and a bare number (`1234`) becomes the substring pattern
`%1234%` on the tracking number. "Jump to ticket" also opens an exact number.
This proposal adds nothing for ticket numbers.

**Finding 1: the status filter and "Include closed" are applied in the
browser, after paging.** `TicketListPage.tsx` gets a page of 50 and then removes
closed tickets and other statuses from it. So a filtered page can show 3 rows
or none, while "Next" is still enabled. A status that only appears on page 4 is
not found on page 1. Adding more filters this way would make the problem worse.
This design moves every filter to the server, and that fixes this problem too.

**Finding 2: a composable query already exists.** `ListTicketsFiltered` (MCP
`list_tickets`) holds the visibility rule as data (`ticket.Visibility`: all,
reporter only, staff scope). Every filter in it is "NULL means absent", so
one prepared statement serves every combination. This design extends that
query and that `ticket.Filter` struct. It does not add a new query.

---

## 1. Filters

### 1.1 The rule that keeps scope safe

Every filter is **ANDed with the caller's visibility**. A filter can only
narrow what the caller can already see. Visibility comes from
`Server.TicketVisibility`, the same function that MCP and `GET /tickets/{id}`
already use, and never from a request parameter:

| Caller | Visibility |
| --- | --- |
| Admin | all tickets |
| Staff, `ticket_scope_enforced` off | all tickets |
| Staff, `ticket_scope_enforced` on | the DESIGN.md staff scope (reported, assigned, group assigned, or Category/Type covered) |
| Reporting user (and any unrecognised role) | own reported tickets |

A filter that names a user, group, category or reporter outside the caller's
scope matches only the visible tickets that also meet the filter. It cannot
return any other ticket. Store tests check this for every filter (section 6).

The one existing exception is kept as it is. A staff member with enforcement
on who names an `assignee_group_id` they are not a member of gets `403`. This
is existing behaviour and it has tests.

### 1.2 Requester-side rule: filter only on fields the requester can see

A requester's response goes through the allowed list
(`ticket.FilterForViewer`). If a requester could filter on a field they are
**never sent** (`updated_at`, `source_channel`, `assignee_group_id`, the SLA
state), the filter would leak that field. For example, `updated_after=…`
narrowed step by step would show when staff last touched the ticket. So a
requester may use a filter **only when its ticket key is on the requester
allowed list**. Any other filter gets `403` (the same pattern as
`assignee_group_id` today). A test checks this rule (section 6).

### 1.3 Requester names and masking

`audit_mask_requester_names` controls what the **audit views** show. It does
not apply to the ticket list. The staff ticket page shows the requester's name
(DESIGN.md: "The ticket page itself still shows the requester's name"). This
design keeps the masking rules as they are because:

- The list response gets **no new fields**. Rows do not carry a requester name.
- The API filters by **id** (`reporter_id`), not by name or email text. The
  picker turns a name or email into an id through `GET /users/lookup`. That
  route is staff and admin only, **signed-in sessions only**
  (`DenyMachineCredentials`, #290), needs at least 2 characters and returns at
  most 10 results. A free-text `requester=` parameter on `/tickets` would be a
  directory search that API keys and OAuth clients could use. #290 ruled that
  out ("there is no user lookup endpoint, so no enumeration surface").
- The audit views are not changed, so `ticket_log` and `admin_log` mask as
  before.

### 1.4 The filters

All parameters are optional. If a parameter is absent, results are not
filtered by it. Multi-value parameters are sent as **repeated keys**
(`status_id=a&status_id=b`), with at most 20 values for each key. Values of the
same key are ORed. Different keys are ANDed.

| Filter | Parameter(s) | Type | Requester | Staff | Admin | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Text / ticket # | `q` | string | yes | yes | yes | Unchanged. |
| Requester (account) | `reporter_id` | uuid | 403 | **yes (new)** | yes | Today admin only. Widened to staff and ANDed with visibility (open question 1). |
| Requester (guest) | `guest_email` | string, exact, case-insensitive | 403 | yes | yes | Guest tickets have no account to pick. Staff already receive `guest_email` on every ticket, so this shows nothing new. |
| Created range | `created_after`, `created_before` | RFC 3339 instant | yes | yes | yes | `created_after` is inclusive and `created_before` is exclusive. `created_at` is on the allowed list. |
| Updated range | `updated_after`, `updated_before` | RFC 3339 instant | 403 | yes | yes | `updated_at` is never sent to requesters. |
| Priority | `priority` (repeatable) | `critical\|high\|medium\|low` | yes | yes | yes | |
| Category | `category_id` | uuid | yes | yes | yes | Matches every type and item under it, because the ticket stores its category. |
| Type | `type_id` | uuid | yes | yes | yes | |
| Item | `item_id` | uuid | yes | yes | yes | |
| Assignee person | `assignee_user_id` | uuid | 403 | yes | yes | Requesters get the id, but they have no reason to filter on it. Refusing it keeps the requester's filter set small. |
| Assignee group | `assignee_group_id` | uuid | 403 | yes* | yes | Existing. *Must be a member when enforcement is on (existing 403). |
| Status | `status_id` (repeatable) | uuid | yes | yes | yes | New on the API. The page moves its status filter here (Finding 1). |
| Include closed | `include_closed` | bool, default `true` | yes | yes | yes | Default `true` keeps current API behaviour. The page sends `false` unless the toggle is on. Ignored when `status_id` is given. It excludes the Closed **system status** by id, not `closed_at`, so a requester's filter depends only on `status_id`. |
| Source channel | `source_channel` (repeatable) | `web\|api\|mcp\|guest\|phone` | 403 | yes | yes | Never sent to requesters. |
| SLA state | `sla` | `breached\|at_risk` | 403 | yes | yes | Matches what the indicator shows (section 2.3). `400` while SLA tracking is off (open question 6). |
| Queue | `scope` | existing | ignored | existing | existing | Existing. |

**How the queue combines with filters.** The handler turns `scope` into a
*queue* (`any`, `mine` or `unassigned`) and ANDs it with everything else:

- `scope=mine` (default): admin, or staff with enforcement off, get queue
  `mine` (assigned to me or to one of my groups). Staff with enforcement on get
  queue `any` inside their scope, as today.
- `scope=unassigned` / `scope=all`: admin only (`403` otherwise), as today.
- `scope=visible`: queue `any`.
- If `reporter_id` or `assignee_group_id` is present, the queue is `any`. This
  keeps today's rule that these two override `scope`. **One small change:**
  today `reporter_id` together with `assignee_group_id` silently drops the
  group, and under this design both apply. Nothing in the app sends both.

**Reply-text search: recommend not in 1.3.0.** What it would cost:

- **Migration.** Add a generated `tsvector` column to `ticket_replies` and a GIN
  index on it. Adding a `STORED` generated column rewrites the whole table under
  an `ACCESS EXCLUSIVE` lock. On an instance with a long history, the upgrade
  would block reads and writes for the length of that rewrite. The index is
  roughly the size of the text it covers.
- **Query.** A reply match is an `EXISTS` on another table. ORing it with the
  ticket's own `search_vector` defeats the BitmapOr that migration 000021 added
  for performance (31 ms down to 0.13 ms at 200k tickets). The query would have
  to be restructured to gather candidate ids from each index and then join, and
  ranking across two tables needs its own decision.
- **Security.** Internal notes must be excluded for requesters. Otherwise a
  requester's search becomes a yes/no test of what an internal note says. That
  rule needs its own tests.
- **Effort.** About 2–3 extra days, plus a measured upgrade time. DESIGN.md
  already lists it as deferred ("Reply bodies are not indexed … Deferred").

---

## 2. SQL

### 2.1 One query

Extend `ListTicketsFiltered`. Do not add a new query. All new filters use the
same pattern: `sqlc.narg` for single values, and a `cardinality(...) = 0`
guard for arrays.

```sql
-- name: ListTicketsFiltered :many
SELECT … FROM tickets t
WHERE
  ( …visibility, unchanged… )
  -- queue: 'any' | 'mine' | 'unassigned'; set by the handler, not the caller
  AND (
    sqlc.arg(queue)::text = 'any'
    OR (sqlc.arg(queue)::text = 'unassigned'
        AND t.assignee_user_id IS NULL AND t.assignee_group_id IS NULL)
    OR (sqlc.arg(queue)::text = 'mine' AND (
        t.assignee_user_id = sqlc.arg(actor_id)::uuid
        OR t.assignee_group_id IN (SELECT gm.group_id FROM group_members gm
                                   WHERE gm.user_id = sqlc.arg(actor_id)::uuid)))
  )
  AND (cardinality(sqlc.arg(status_ids)::uuid[]) = 0 OR t.status_id = ANY(sqlc.arg(status_ids)::uuid[]))
  AND (sqlc.narg(exclude_status_id)::uuid IS NULL OR t.status_id <> sqlc.narg(exclude_status_id)::uuid)
  AND (cardinality(sqlc.arg(priorities)::text[]) = 0 OR t.priority = ANY(sqlc.arg(priorities)::text[]))
  AND (sqlc.narg(category_id)::uuid IS NULL OR t.category_id = sqlc.narg(category_id)::uuid)
  AND (sqlc.narg(type_id)::uuid     IS NULL OR t.type_id     = sqlc.narg(type_id)::uuid)
  AND (sqlc.narg(item_id)::uuid     IS NULL OR t.item_id     = sqlc.narg(item_id)::uuid)
  AND (sqlc.narg(assignee_user_id)::uuid  IS NULL OR t.assignee_user_id  = sqlc.narg(assignee_user_id)::uuid)
  AND (sqlc.narg(assignee_group_id)::uuid IS NULL OR t.assignee_group_id = sqlc.narg(assignee_group_id)::uuid)
  AND (sqlc.narg(reporter_id)::uuid IS NULL OR t.reporter_user_id = sqlc.narg(reporter_id)::uuid)
  AND (sqlc.narg(guest_email)::text IS NULL OR lower(t.guest_email) = lower(sqlc.narg(guest_email)::text))
  AND (sqlc.narg(created_after)::timestamptz  IS NULL OR t.created_at >= sqlc.narg(created_after)::timestamptz)
  AND (sqlc.narg(created_before)::timestamptz IS NULL OR t.created_at <  sqlc.narg(created_before)::timestamptz)
  AND (sqlc.narg(updated_after)::timestamptz  IS NULL OR t.updated_at >= sqlc.narg(updated_after)::timestamptz)
  AND (sqlc.narg(updated_before)::timestamptz IS NULL OR t.updated_at <  sqlc.narg(updated_before)::timestamptz)
  AND (cardinality(sqlc.arg(source_channels)::text[]) = 0 OR t.source_channel = ANY(sqlc.arg(source_channels)::text[]))
  AND ( …sla, section 2.3… )
  AND ( …search, unchanged (the `searching` flag)… )
ORDER BY
  CASE WHEN sqlc.arg(search_query)::text <> '' THEN ts_rank(…) ELSE 0 END DESC,
  t.created_at DESC,
  t.id                      -- new tiebreak; the Go merge had one, the SQL did not
LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);
```

### 2.2 Keeping sqlc honest with optional filters

- **Single values** use `sqlc.narg(x)::type`. sqlc generates a `uuid.NullUUID`,
  a `sql.NullString` or a `sql.NullTime`. The `::type` cast is required, because
  without it sqlc cannot infer the type from `$n IS NULL`.
- **Arrays** use `sqlc.arg(x)::uuid[]` with a `cardinality(...) = 0` guard, not
  `IS NULL`. The store always passes a **non-nil empty slice** for "absent".
  I have not checked how `pq.Array` encodes a nil slice through pgx stdlib.
  The `cardinality` guard avoids depending on it, and a store test covers
  "absent array = no filter". `statuses.sql` already passes a `uuid[]` this way.
- **The queue is an enum string, not three booleans.** One value cannot be in
  two states at once.
- **One test per filter in the store (section 6)** passes only that filter and
  checks that it narrows the results. A second test passes no filters and
  checks the result against `ListAllTickets`. A typo in a `narg` name produces a
  different generated field, which fails the build. A wrong column shows up as
  a test failure.

### 2.3 SLA state in SQL

The indicator is computed in Go (`sla.StatusFor`, `colorFor`: red at ≥100%,
amber at ≥80%). A filter has to run in SQL, or pages come back short. Pause-aware
elapsed time is already expressed in SQL in `ListSLABreachCandidates`, and that
query notes that it and `sla.ElapsedFrom` "must change together". The filter
uses the same expression on the **current** record (`superseded_at IS NULL`):

- **`breached`** (the indicator is red): `response_breached_at IS NOT NULL OR
  resolution_breached_at IS NOT NULL`, **or** a target that is still
  outstanding has reached its deadline (the same `<=` comparison against
  `LEAST(COALESCE(pending_since, now), now)` as the breach sweep). This covers
  the time between a breach and the next sweep tick, as the indicator does.
- **`at_risk`** (amber): not breached, and some outstanding target has passed
  80%. The deadline is `target_min * 48` seconds, which is exactly 80% of the
  target in whole seconds. That matches `colorFor`'s integer `elapsed*5 >=
  target*4`, with no floating point.
- `now` is a parameter. Tests pass a fixed instant.

This is the **third** copy of the elapsed-time formula. The safeguard is a test
(section 6), not a SQL function. The test builds tickets at 79.9%, 80%, 99.9%
and 100% of target, while pending, after a pause and after a restart, and
checks that the SQL filter and `sla.StatusFor` put each ticket in the same
bucket. One known edge: a target met before the frozen-elapsed column existed,
which migration 000028 could only estimate, can read red in Go and have no
breach stamp. The test documents this case and does not hide it.

### 2.4 Indexes and migration 000060

**Newest migration: `000059_custom_field_retired_options`** on
`origin/v1.3.0-beta`. I checked every remote branch, and no branch has 000060
or higher. The open PRs are #613, #588, #577, #576 and #430. The two dependabot
branches are at 000022, the docs branches have no migrations, and
`cf-a-design` is at 000058. **000060 is free.**

Existing indexes already cover: `reporter_user_id`, `assignee_user_id`,
`assignee_group_id`, `status_id`, `category_id`, `created_at DESC`, the
search vector, tracking-number trigrams, and `sla_records` by ticket.

**000060 adds two indexes** (each `down` drops them; no data is lost):

```sql
CREATE INDEX IF NOT EXISTS tickets_updated_at_idx ON tickets (updated_at DESC);
CREATE INDEX IF NOT EXISTS tickets_guest_email_lower_idx
    ON tickets (lower(guest_email)) WHERE guest_email IS NOT NULL;
```

Not added: `priority` and `source_channel` (few distinct values, so the planner
would ignore the index), and `type_id` and `item_id` (almost always used with
`category_id`, which is indexed). PR B measures this at 200k tickets with the
method of migration 000021 and adds an index only if a measurement shows the
need.

**Risk: generic plans.** The pool uses pgx's default statement cache. Postgres
can switch a cached statement to a generic plan after 5 runs. A generic plan for
`($1 IS NULL OR col = $1)` cannot choose an index for the one filter that is
actually set. MCP already runs `ListTicketsFiltered` this way, but MCP traffic
is light. PR B measures the 6th and later runs, not only the first. If plans
get worse, the fix is one line: `plan_cache_mode=force_custom_plan` in the
pool's runtime parameters. That applies to every query, so it is open question
7 and not a decision to make quietly.

---

## 3. API

### 3.1 Handler shape

`handleListTickets` becomes: **parse → check each filter against the role →
resolve visibility and queue → `ListFiltered` → `writeTickets`.** That replaces
the seven branches and the 1+N merge. Parsing and the role check are a pure
domain function (`ticket.ParseListFilter` / `Filter.AllowedFor(role)`), so REST
and MCP share one definition and cannot drift apart.

**Behaviour that stays the same:** the same tickets for every existing
parameter combination (except the `reporter_id` + `assignee_group_id` pair in
1.4), the same 403s and 400s with the same messages, the same `limit`/`offset`
rules (`pageParams`), and a bare array as the response.

**Behaviour that changes:** a search on the `mine` queue (admin, or staff with
enforcement off) is ranked by relevance like every other search, instead of
newest first. DESIGN.md's Ticket Search already says results are ranked. The
OpenAPI text documents the exception and would be updated. Open question 4.

The 12 single-purpose list and search queries lose their REST callers. They are
removed in a **separate commit** after checking that nothing else calls them
(CLAUDE.md "No breaking changes").

### 3.2 Validation

| Case | Response |
| --- | --- |
| Not a UUID, unknown enum value, unparseable instant, bad `include_closed` | `400 bad_request`, `"invalid <param>"` (the existing wording) |
| `created_after` ≥ `created_before` (same for `updated_*`) | `400 bad_request`, `"created_after must be before created_before"` |
| More than 20 values for a repeatable key | `400 bad_request`, `"too many status_id values (max 20)"` |
| `guest_email` with no `@`, or longer than 254 characters | `400 bad_request` |
| Filter not allowed for the role (section 1.2) | `403 forbidden`, `"<param> is not available to requesters"`; existing 403 texts unchanged |
| `sla` while SLA tracking is off | `400 bad_request`, `"SLA tracking is off"` |
| An id that names nothing (deleted category, unknown user) | `200 []`. No existence check: an empty result is the true answer. A check would cost a query and would show whether ids exist to callers who cannot see them. |
| Unknown parameter | ignored, as today |

Validation runs **before** any query. A request that is refused costs no
database time.

### 3.3 OpenAPI (`docs/api/openapi.yaml`, `GET /api/v1/tickets`)

- Add the 14 parameters from 1.4 with `style: form`, `explode: true` for the
  repeatable ones. Mark each parameter's description with who may use it.
- Rewrite the description paragraph "Four parameters can change the listing…
  The first one present wins". Filters combine with AND, and the queue rule is
  in 1.4.
- Remove the "except for the default listing … stays newest first" sentence
  (if open question 4 is approved).
- Add the new 400 cases and the new 403 text.
- Add a parameter to `/users/lookup`: `id` (open question 3).
- DESIGN.md: rewrite **List filters** and update **Ticket Search** in the same
  PR.

---

## 4. UI

### 4.1 Behaviour

- **The URL holds the state.** `validateSearch` on `/tickets` gains `q`,
  `scope`, `status` (array), `priority` (array), `category`, `type`, `item`,
  `assignee`, `group`, `reporter`, `guest`, `created_from`, `created_to`,
  `updated_from`, `updated_to`, `channel` (array), `sla`, `closed` and `page`.
  The `status=<id>` and `reporter=<id>` links that exist today (Dashboard, user
  page) keep working. The search box writes `q` with `replace` after the
  300 ms debounce, so typing does not fill the browser history. A copied link
  reproduces the search, including the page.
- **Dates.** The URL holds `YYYY-MM-DD`. The page converts each date to an
  instant in the **viewer's** timezone (`created_from` → start of that day,
  `created_to` → start of the next day, so the end date is included). A link
  opened in another timezone moves by the offset (open question 8).
- **Filters panel.** A "Filters" button sits next to the search box and shows
  a count of active filters. On `md` and wider, the panel opens inline below
  the toolbar as a grid. Below `md`, it opens as a full-screen dialog (Radix
  Dialog, already a dependency) with **Apply** and **Clear** buttons. Both
  layouts use one form component. No new dependencies: native
  `<input type="date">` and checkbox groups for multi-value filters.
- **Fields by role.** Requesters see Status, Priority, Category/Type/Item and
  Created. Staff and admin see everything. The SLA field is hidden while SLA
  tracking is off.
- **Requester picker.** Reuses `RequesterPicker` (New Ticket page,
  `/users/lookup`). If the text typed is an email address and matches no
  account, the picker also offers "Guest tickets from <address>", which sets
  `guest`.
- **Chips.** Each active filter shows as a chip ("Status: Open, Pending ×",
  "Created: 1 Oct – 31 Oct ×", "Requester: Dana Smith ×"), followed by **Clear
  all**. Chips wrap and never scroll sideways. These replace the two separate
  "Filtering by status" and "Filtering by client" rows. A chip's name comes
  from data the page already loads (statuses, categories, groups, `/staff`).
  The requester's name comes from `/users/lookup?id=` (open question 3). If
  the lookup fails, the chip shows "Requester ab12…".
- **The "Only <queue>" chip** (#444) is unchanged. Any filter, not only text,
  now counts as "searching" for the wide search. The reason: with a filter set,
  a staff member expects it to search everything they can see, not just their
  queue. This changes UI behaviour, but only once a filter is set, and no
  filter existed before.
- **Empty result:** "No tickets match these filters." with a **Clear all**
  link.

### 4.2 Desktop wireframe (≥ md)

```
Tickets                                                        [+ New Ticket]
┌──────────────────────────────────────────┐ [Mine|Unassigned|All] [Filters (3) ▾]
│ 🔍 Search by tracking number, subject…   │ ☐ Include closed   [Jump to ticket]
└──────────────────────────────────────────┘
┌ Filters ──────────────────────────────────────────────────────────────────────┐
│ Status          Priority        Requester               Assignee              │
│ ☑ Open          ☐ Critical      [Dana Smith        ×]   [Any person      ▾]   │
│ ☑ Pending       ☑ High                                  Group                 │
│ ☐ Resolved      ☐ Medium        Category  [Hardware ▾]  [Any group       ▾]   │
│ ☐ Closed        ☐ Low           Type      [Printer  ▾]                        │
│                                 Item      [Any      ▾]  Channel ☐web ☐api     │
│ Created  [2026-10-01] – [2026-10-31]                    ☐mcp ☐guest ☐phone    │
│ Updated  [          ] – [          ]                    SLA [Any ▾]           │
│                                                    [Clear all]  [Close panel] │
└───────────────────────────────────────────────────────────────────────────────┘
Status: Open, Pending ×  Priority: High ×  Requester: Dana Smith ×  Category: Hardware › Printer ×
Created: 1 Oct – 31 Oct ×                                                     Clear all
Searching all tickets you can see   (Only my tickets)
┌───┬──────────────────┬──────────────────────────┬──────────┬──────────┬───────┐
│ ☐ │ Ticket           │ Subject                  │ Status   │ Priority │ SLA   │
│ … │ GHD-2026-000412  │ Printer jams on tray 2   │ ● Open   │ high     │ ● 82% │
└───┴──────────────────┴──────────────────────────┴──────────┴──────────┴───────┘
                                     [Previous]  Page 1  [Next]
```

Filters apply as soon as they are changed (no Apply button on desktop, as with
the search box).

### 4.3 Phone wireframe (390 px)

```
┌──────────────────────────────┐      ┌──────────────────────────────┐
│ Tickets        [+ New Ticket]│      │ Filters                    ✕ │
│ ┌──────────────────────────┐ │      │──────────────────────────────│
│ │🔍 Search…                │ │      │ Status                       │
│ └──────────────────────────┘ │      │ ☑ Open      ☑ Pending        │
│ [Filters (3)] ☐ Incl. closed │ tap  │ ☐ Resolved  ☐ Closed         │
│                              │ ───► │ Priority                     │
│ Status: Open, Pending ×      │      │ ☐ Critical ☑ High ☐ Med ☐ Low│
│ Priority: High ×             │      │ Requester                    │
│ Created: 1–31 Oct ×          │      │ [Dana Smith              ×]  │
│ Clear all                    │      │ Category  [Hardware      ▾]  │
│ ┌──────────────────────────┐ │      │ Type      [Printer       ▾]  │
│ │GHD-2026-000412   ● Open  │ │      │ Created from [2026-10-01]    │
│ │Printer jams on tray 2    │ │      │ Created to   [2026-10-31]    │
│ │[high] ● 82%              │ │      │ … (Updated, Assignee, Group, │
│ └──────────────────────────┘ │      │    Channel, SLA)             │
│ [Previous]  Page 1   [Next]  │      │──────────────────────────────│
└──────────────────────────────┘      │ [Clear]              [Apply] │
                                      └──────────────────────────────┘
```

On a phone, the dialog collects changes and applies them all at once with
**Apply**, so each tap does not reload the list behind the dialog. The admin
Mine/Unassigned/All switch moves into the dialog below `md`. No bulk selection
on a phone (unchanged).

---

## 5. MCP

**Recommendation: yes, `list_tickets` gets the same filters in 1.3.0.** It
already calls `ListFiltered` with a `ticket.Filter`. `buildListFilter` gains
the same parameters (arrays as JSON arrays) through the **same domain parse and
role check** as REST, so a requester using MCP is refused the same filters with
the same reasons. No queue parameter: MCP has always listed everything visible.
`status_id` (single) is kept, and `status_ids` is added next to it, so existing
callers still work. The default limit of 20 and the maximum of 100 are
unchanged. `TicketTools` classification is unchanged (the tool already returns
tickets through the requester filter).

---

## 6. Tests

Every PR writes its failing tests first (CLAUDE.md).

**Go, domain (no DB)** in `internal/domain/ticket/filter_test.go`:
- Parse table: each parameter valid, invalid, empty and repeated; the
  20-value limit; inverted ranges; and the `include_closed` default.
- `TestListFilter_RequesterMayOnlyFilterOnAllowedKeys`: every filter field is
  mapped to the ticket key it reads, and a requester is allowed a filter **only
  if** that key is on the requester allowed list. A new filter with no mapping
  fails the test. This follows the same idea as
  `TestRequesterKeys_EveryTicketKeyIsClassified`.
- Role table: each filter × {user, staff, admin, unknown role}. An unknown role
  is treated as a requester.

**Go, store (real Postgres)** in `internal/database/ticketstore/ticketstore_test.go`:
- One test per filter: it narrows the results, and an absent filter (nil or
  empty slice) does not.
- The no-filter result equals `ListAllTickets`, and the `mine` queue equals the
  old 1+N merge on the same fixture (this checks the replacement before the
  old code is removed).
- **Scope:** with `VisibilityScoped`, every filter is run while naming an
  out-of-scope ticket's own reporter, assignee, group, category, dates,
  channel and SLA state. The out-of-scope ticket never appears.
- SLA buckets vs `sla.StatusFor` (section 2.3), including pending, paused,
  restarted and superseded records.
- Paging: a filtered result gives full pages, and the `id` tiebreak keeps rows
  with equal timestamps in a fixed order across pages.
- Migration 000060 up, down and up again.

**Go, handler (`httptest`)** in a new `internal/server/handler_tickets_filters_test.go`:
- Every existing parameter combination returns what it returned before. These
  tests are written against the **current** handler first, so they prove the
  rewrite changes nothing (the existing `handler_tickets_search_visible_test.go`
  and `handler_source_channel_test.go` must also pass unchanged).
- The validation table in 3.2: status codes and error JSON.
- Staff with enforcement on: `reporter_id`, `assignee_user_id`,
  `category_id` and `sla` naming out-of-scope things return only in-scope
  tickets. A non-member `assignee_group_id` still returns 403.
- Requester: each staff-only filter returns 403. Allowed filters return only
  their own tickets. The response body has no never-listed key.
- API key and OAuth client: same results as the owner's role. `/users/lookup?id=`
  returns `403 session_required`.
- `sla` while tracking is off returns 400.

**MCP** in `internal/mcp`: `buildListFilter` table for the new arguments; a
requester is refused the same filters as over REST (one shared table run
against both).

**vitest**:
- URL ↔ filter state round trip, including old `status=`/`reporter=` links and
  date to instant conversion at a DST boundary.
- Chips render and clear one at a time, and Clear all works.
- Requester role sees only its four fields. SLA field hidden while tracking is
  off.
- `listTickets` sends repeated keys.
- The requester chip falls back when the lookup fails.
- Paging resets when a filter changes.

**Playwright** (mocked API, `frontend/e2e/ticket-filters.spec.ts`):
- At desktop: set status and a date range, check the chips and the URL, reload
  and check that the same filters and results come back, then Clear all.
- At 390 px: open the dialog, Apply, check the chips wrap with **no horizontal
  scroll** (as `mobile-layout.spec.ts` does), and the dialog closes.
- A shared link opened in a fresh page shows the same filtered list.

---

## 7. Work, order and estimate

| PR | Owns (files) | Depends on |
| --- | --- | --- |
| **A. Filter contract** | `internal/domain/ticket/filter.go`, `filter_test.go` | — |
| **B. Query, store, migration** | `queries/tickets.sql` (`ListTicketsFiltered` only), `internal/dbgen/*` (generated), `internal/database/ticketstore/ticketstore.go` + tests, `migrations/000060_ticket_filter_indexes.{up,down}.sql`, plan measurements in the PR body | A |
| **C. REST handler + docs** | `internal/server/handler_tickets.go` (`handleListTickets` only), `handler_tickets_filters_test.go`, `docs/api/openapi.yaml` (`/tickets`), `docs/DESIGN.md` (Ticket Search, List filters) | A; merges after B |
| **C2. Remove dead queries** (separate commit/PR) | `queries/tickets.sql` (the 12 old queries), `ticketstore.go`, `ticket` service and `Store` interface methods with no callers | C |
| **D. Lookup by id** | `handler_admin_users.go` (`handleLookupUsers`), `queries/users.sql`, test, OpenAPI `/users/lookup` | — |
| **E. MCP** | `internal/mcp/server.go` (`list_tickets`, `buildListFilter`), tests | A; merges after B |
| **F. Frontend** | `frontend/src/api/tickets.ts`, `router.tsx` (`/tickets` search), `pages/TicketListPage.tsx`, new `components/ticket/TicketFilters.tsx` + `FilterChips.tsx`, vitest files, `e2e/ticket-filters.spec.ts`, `e2e/mock.ts` | contract in this doc; merges after C and D |

**Order:** A first (small, about half a day; it fixes the struct everyone
codes against). Then B, C, D, E and F run **in parallel**, each against the
contract. B merges before C and E, because they call the new query. F works
against `e2e/mock.ts` and merges last. C2 follows C. File ownership does not
overlap except `openapi.yaml`, which C and D edit in different sections.

**Estimate (honest):**

| PR | Effort |
| --- | --- |
| A | 0.5 day |
| B | 2 days (the SLA agreement test and the 200k-row measurement take most of it) |
| C | 2 days (the "unchanged behaviour" tests come first and are most of the work) |
| C2 | 0.5 day |
| D | 0.5 day |
| E | 0.5 day |
| F | 3 days (panel, dialog, URL state, chips, picker reuse, tests) |
| **Total** | **≈ 9 working days of coding**; with 3–4 coders in parallel, **about 4–5 calendar days to merged**, including review rounds and the review gate before each merge |

Reply-text search would add 2–3 days and a migration with a slow upgrade. It
is not included.

**Schedule risk:** this comes before the tag, together with the pre-tag audit
fix PRs. C changes the most-used endpoint in the app. If the timeline is
tight, the smallest version that still delivers the request is A + B + C
(without the `mine` unification: see open question 4, option b) + F.

---

## 8. Open questions (each with a recommendation)

1. **Should `reporter_id` be opened to staff?** It is admin only today, so
   staff could not filter by requester at all.
   *Recommend: yes.* The filter is ANDed with visibility, so it cannot show a
   ticket the staff member could not already open, and staff already receive
   `reporter_user_id`. A requester still gets 403.

2. **Should requester search accept free text (name or email) on `/tickets`?**
   *Recommend: no.* Take ids (`reporter_id`) plus the exact `guest_email`. The
   name and email search happens in the picker through `/users/lookup`, which
   is session-only. Free text on `/tickets` would give API keys and OAuth
   clients the directory search that #290 deliberately removed.

3. **What should the requester chip show after a reload or on a shared link?**
   The URL holds an id. Staff have no way to turn an id into a name today
   (`listUsers` is admin only).
   *Recommend: add `GET /users/lookup?id=<uuid>`.* It is session-only and
   excludes the same accounts (disabled, deleted, service), with a fallback to
   "Requester ab12…". Do not put the name in the URL: a shared link could then
   say "Dana" while it filters for someone else.

4. **Should every list request go through the one query, which makes a search
   on the `mine` queue ranked instead of newest first?**
   *Recommend: (a) yes.* It removes 7 branches and a 1+N merge, and it matches
   DESIGN.md's "ranked by relevance". (b) The alternative: keep the old
   branches when no new filter is present, and use the new query only when
   one is. That has a smaller effect before the tag but leaves two paths that
   can drift apart.

5. **Reply-text search in 1.3.0?**
   *Recommend: no.* Defer it, as DESIGN.md already does. Cost: a table rewrite
   on upgrade, a query restructure, internal-note tests, and 2–3 days.

6. **What should `sla=` do while SLA tracking is off?**
   *Recommend: `400 "SLA tracking is off"`, with the field hidden in the UI.*
   An empty list would look like "nothing is breached", which is a false
   answer.

7. **What if the measurement shows bad generic plans?**
   *Recommend: measure first in PR B.* If plans get worse, set
   `plan_cache_mode=force_custom_plan` on the pool. It costs a little planning
   time on every query, and it is a one-line change that is easy to undo. Do
   not switch to SQL built per request, which would give up sqlc.

8. **Which timezone should dates in a shared link use?**
   *Recommend: the viewer's timezone, and accept the shift.* The help desk has
   no per-user or instance timezone setting. Adding one only for this is out
   of scope.

9. **Should tags, custom fields or sort order be filters too?**
   They were not asked for, and DESIGN.md already defers saved searches (the
   URL now covers that).
   *Recommend: not in 1.3.0.* The `Filter` struct and the query take them
   later without any changes to the design.

10. **Should a filter widen a staff search to "all tickets you can see" (4.1),
    as text in the box does?**
    *Recommend: yes.* It is the same rule as #444. The "Only <queue>" chip
    narrows it.
