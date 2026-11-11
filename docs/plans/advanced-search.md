# Advanced ticket search: design proposal (1.3.0)

Status: **proposal, partly decided.** Nothing in this document is to be built
until the owner approves it. Base: `origin/v1.3.0-beta` at `bc2f7a14`.

The request: search tickets "by requester, by date range, by ticket #, etc."
This proposal adds filters and reply-text search to `GET /api/v1/tickets`, the
ticket list page and MCP `list_tickets`, using one SQL query for all of them.

## Decisions so far (Erik, 2026-11-10)

| # | Question | Decision |
| --- | --- | --- |
| D1 | Reply-text search in 1.3.0? | **"Yes, in 1.3.0."** Designed in full in section 2.5. It has its own PR (R). |
| D2 | Open `reporter_id` to staff? | **"Yes, within visibility."** |
| D3 | Free-text requester name or email? | **"Free text on web, api keys need explicit freetext search permission."** A browser session may send free text. API keys and OAuth clients need a new explicit scope (section 1.3). The id-based filter stays. |
| D4 | The UI | **"Show me wireframes first."** A static mock is in `searchmock/` (section 4). Every other recommendation here is **not yet approved**. |

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
not found on page 1. This design moves every filter to the server, and that
fixes this problem too.

**Finding 2: a composable query already exists.** `ListTicketsFiltered` (MCP
`list_tickets`) holds the visibility rule as data (`ticket.Visibility`: all,
reporter only, staff scope). Every filter in it is "NULL means absent", so
one prepared statement serves every combination. This design extends that
query and that `ticket.Filter` struct. It does not add a new list query.

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

A filter that names a user, group, category or requester outside the caller's
scope matches only the visible tickets that also meet the filter. It cannot
return any other ticket. Store tests check this for every filter (section 6).

The one existing exception is kept as it is. A staff member with enforcement
on who names an `assignee_group_id` they are not a member of gets `403`. This
is existing behaviour and it has tests.

### 1.2 Requesters may filter only on fields they can see

A requester's response goes through the allowed list
(`ticket.FilterForViewer`). If a requester could filter on a field they are
**never sent** (`updated_at`, `source_channel`, `assignee_group_id`, the SLA
state), the filter would leak that field. For example, `updated_after=…`
narrowed step by step would show when staff last touched the ticket. So a
requester may use a filter **only when its ticket key is on the requester
allowed list**. Any other filter gets `403`, the same pattern as
`assignee_group_id` today. A test checks this rule (section 6).

### 1.3 Requester filter: by id, and by free text (D2, D3)

There are two parameters:

- **`reporter_id=<uuid>`**: the tickets one account reported. Today this is
  admin only. Per D2 it is opened to staff, ANDed with visibility. A requester
  still gets 403. Any credential may use it with `tickets:read`.
- **`requester=<text>`** (new): free text, 2 to 254 characters after
  trimming. It matches a ticket when the text appears, ignoring case, in any
  of these:
  - the reporting account's display name,
  - the reporting account's email address,
  - the ticket's `guest_name`,
  - the ticket's `guest_email`.

  Deleted and disabled reporting accounts are included, because their tickets
  still exist and the staff ticket page still names them. The text is
  LIKE-escaped the way `/users/lookup` already escapes it. This one parameter
  finds both guests and account holders, so the earlier draft's `guest_email`
  parameter is dropped.

**Who may send `requester` text:**

| Caller | `requester=` |
| --- | --- |
| Browser session, staff or admin | allowed |
| Browser session, requester | `403 forbidden`: "requester is not available to requesters" (it is always themselves) |
| API key or OAuth client **with** `requester_search:read` | allowed |
| API key or OAuth client **without** it | `403 insufficient_scope`: "this credential does not carry the requester_search:read scope". This is the same code and wording the scope middleware already uses (`internal/middleware/auth.go`). The `reporter_id` filter stays open to it. |

**The new scope: `requester_search:read`.** It fits the existing model
(DESIGN.md, Credential Scopes) like this:

- It is a **resource with only a read action**, the same shape as `audit`.
  `requester_search:write` is refused as an unknown scope.
- **It is not implied by anything.** Neither `tickets:read` nor `users:read`
  grants it. Rule 4 of the scope model (no wildcard) is the reason. A key
  that already exists does not gain free-text search when 1.3.0 ships. Its
  owner has to grant it on purpose.
- **Who may be granted it:** a key owned by **staff or an administrator**, and
  an OAuth client whose service user is **staff**. A requester never owns a
  key (#450). An OAuth client whose service user is a reporting user would
  only ever get 403 from it, so `WithinRole` refuses to grant it. This means
  adding it to `ScopesForRole` for `staff` and `admin` (in `admin` through
  `All()`).
- **Where it is checked:** in the handler, on the parameter, and in
  `list_tickets` on the argument. It is not checked per route, because the
  route is the same `GET /tickets`. This is the first scope that gates a
  parameter instead of a route.
  `TestScopesForRole_MatchTheRouter` derives the scope table from the router,
  so it would report this scope as unused. PR S adds a short, named list of
  parameter scopes that the test reads too, and adds a test of its own: every
  entry on that list must be checked by at least one handler test.
- **Why a scope and not "sessions only".** Free text over many visible
  tickets maps names and addresses to tickets in bulk. A key can already do
  that one ticket at a time: `GET /tickets/{id}` gives staff the `requester`
  line. But #290 removed every directory search from machine credentials, and
  D3 lets an owner allow it again only on purpose.
- **What the scope does not do:** it does not open `GET /users/lookup` to
  keys. That route stays session-only (#290). The scope covers the `requester`
  text filter on `/tickets` and in `list_tickets`, and nothing else.

The API-key and OAuth-client pages list the new scope with this help text:
"Search tickets by requester name or email (free text)".

### 1.4 Requester names and masking

`audit_mask_requester_names` (`admin_log`, `ticket_log` or `everywhere`)
controls what the **audit views** show. Its label is "Mask requester names in
Audit Trail". DESIGN.md says the mask is display-only and "The ticket page
itself still shows the requester's name". What this design does with it:

- **The audit views are unchanged.** With masking on at any value, the admin
  log and the ticket Activity feed still show "Requester (name hidden)". A
  search writes no audit entry, so no name gets into one.
- **The list response gets no names.** Rows carry no requester name or
  address. Reply-match snippets (section 2.5) say where the words were found
  ("Public reply", "Internal note") and when, **never who wrote them**.
- **The chip shows only what the viewer gave.** "Requester contains
  'whitfield'" repeats the viewer's own text. "Requester: Dana Whitfield"
  comes from the picker, which is the same `/users/lookup` result the New
  Ticket page shows.
- **What masking does not stop, and did not stop before.** A staff member
  who can see a ticket can learn its requester's name: from the ticket page,
  and now also by asking which visible tickets match a name. That is the
  display-only rule DESIGN.md already states. The mask is not an access
  control, and this design does not make it one.

If the owner wants `everywhere` to also turn off name matching, that would
change what the setting means. It is open question 5.

### 1.5 All filters

All parameters are optional. If a parameter is absent, results are not
filtered by it. Multi-value parameters are sent as **repeated keys**
(`status_id=a&status_id=b`), with at most 20 values for each key. Values of the
same key are ORed. Different keys are ANDed.

| Filter | Parameter(s) | Type | Requester | Staff | Admin | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Text / ticket # | `q` | string | yes | yes | yes | Unchanged. |
| Search replies too | `search_replies` | bool, default `false` | yes (public replies only) | yes (replies and internal notes) | yes | New. Section 2.5. Has no effect without `q`. |
| Requester (account) | `reporter_id` | uuid | 403 | **yes (D2)** | yes | ANDed with visibility. |
| Requester (free text) | `requester` | string, 2–254 chars | 403 | yes (keys need `requester_search:read`) | yes (same) | D3, section 1.3. |
| Created range | `created_after`, `created_before` | RFC 3339 instant | yes | yes | yes | `created_after` is inclusive and `created_before` is exclusive. |
| Updated range | `updated_after`, `updated_before` | RFC 3339 instant | 403 | yes | yes | `updated_at` is never sent to requesters. |
| Priority | `priority` (repeatable) | `critical\|high\|medium\|low` | yes | yes | yes | |
| Category | `category_id` | uuid | yes | yes | yes | Matches every type and item under it. |
| Type | `type_id` | uuid | yes | yes | yes | |
| Item | `item_id` | uuid | yes | yes | yes | |
| Assignee person | `assignee_user_id` | uuid | 403 | yes | yes | |
| Assignee group | `assignee_group_id` | uuid | 403 | yes* | yes | Existing. *Must be a member when enforcement is on (existing 403). |
| Status | `status_id` (repeatable) | uuid | yes | yes | yes | New on the API. The page moves its status filter here (Finding 1). |
| Include closed | `include_closed` | bool, default `true` | yes | yes | yes | Default `true` keeps current API behaviour. The page sends `false` unless the toggle is on. Ignored when `status_id` is given. It excludes the Closed **system status** by id. |
| Source channel | `source_channel` (repeatable) | `web\|api\|mcp\|guest\|phone` | 403 | yes | yes | Never sent to requesters. |
| SLA state | `sla` | `breached\|at_risk` | 403 | yes | yes | Matches what the indicator shows (section 2.3). |
| Queue | `scope` | existing | ignored | existing | existing | Existing. |

**How the queue combines with filters.** The handler turns `scope` into a
*queue* (`any`, `mine` or `unassigned`) and ANDs it with everything else:

- `scope=mine` (default): admin, or staff with enforcement off, get queue
  `mine` (assigned to me or to one of my groups). Staff with enforcement on get
  queue `any` inside their scope, as today.
- `scope=unassigned` / `scope=all`: admin only (`403` otherwise), as today.
- `scope=visible`: queue `any`.
- If `reporter_id`, `requester` or `assignee_group_id` is present, the queue
  is `any`. This keeps today's rule that `reporter_id` and
  `assignee_group_id` override `scope`.
- **One small change:** today `reporter_id` together with `assignee_group_id`
  silently drops the group, and under this design both apply. Nothing in the
  app sends both.

### 1.6 Sort (owner decision S9)

Every visible column of the list can be sorted, in either direction, by
clicking its header. The page keeps the sort in the URL. It is not a filter: it
changes the order, never which tickets match. The contract is in
`ticket.Filter` (`Sort`, `Order`, `ResolvedSort()`).

| Parameter | Values | Default |
| --- | --- | --- |
| `sort` | `number`, `subject`, `status`, `priority`, `sla`, `created`, `updated` | absent (see "Default", below) |
| `order` | `asc`, `desc` | the column's natural direction |

**Default, and how it combines with `q`.**
- No `sort` and no `q`: `created`, newest first (what every listing has always done).
- No `sort` and a `q`: **relevance**, best match first. This is the only time
  relevance applies.
- A `sort` replaces relevance. The text still narrows the results but no
  longer orders them.
- `sort=relevance` is **not** accepted (400). Dropping `sort` is how the page
  returns to relevance.
- `order` without `sort` is ignored. A value other than `asc` or `desc` is 400
  even then, so a typo is not hidden.

**Natural direction** (used when `order` is absent; the page always sends one
explicitly): `created` and `updated` newest first (`desc`). `number`,
`subject`, `status`, `priority` and `sla` are `asc`, where for priority and SLA
`asc` means most urgent first.

**What each column orders by** (the store implements exactly this):

| `sort` | Orders by |
| --- | --- |
| `number` | `tracking_number`, as text. The year and the zero-padded sequence make that creation order within a prefix. |
| `subject` | `lower(subject)` |
| `status` | the status's `sort_order` (the order administrators arranged them in), then its name |
| `priority` | `critical`, `high`, `medium`, `low`; `asc` is critical first |
| `sla` | urgency: breached, then at risk, then everything else (on track, no SLA, tracking off); `asc` is most urgent first. Same buckets as the `sla` filter (section 2.3). |
| `created` | `created_at` |
| `updated` | `updated_at` |

**Tie-break.** Every sort ends with `created_at DESC, id`, so equal rows keep a
stable order across pages. This replaces the fixed `ORDER BY` in section 2.1.

**Requesters.** Ordering by a field a requester is never sent would show them
its order, the same leak as filtering on it (section 1.2). A requester may sort
by `number`, `subject`, `status`, `priority` and `created`. `sort=updated` and
`sort=sla` are `403 forbidden`, "sort by updated is not available to
requesters". `sla` while SLA tracking is off is `400`, "SLA tracking is off",
as for the `sla` filter (section 3.2).

**Unknown values fail closed.** `ParseListFilter` refuses an unknown `sort` or
`order` with `400`. `Filter.AllowedFor` refuses them again, for every role,
because MCP builds a `Filter` from a map and may not go through the parser.

---

## 2. SQL

### 2.1 One list query

Extend `ListTicketsFiltered`. Do not add a new list query. Every new filter
follows one of two patterns: `sqlc.narg` for single values, and a
`cardinality(...) = 0` guard for arrays.

```sql
-- name: ListTicketsFiltered :many
WITH reply_hits AS (                       -- section 2.5; empty unless asked for
  SELECT DISTINCT r.ticket_id FROM ticket_replies r
  WHERE sqlc.arg(search_replies)::bool
    AND sqlc.arg(search_query)::text <> ''
    AND to_tsvector('english', r.body) @@ to_tsquery('english', sqlc.arg(search_query)::text)
    AND (sqlc.arg(include_internal)::bool OR NOT r.internal)
)
SELECT … FROM tickets t
WHERE
  ( …visibility, unchanged… )
  -- queue: 'any' | 'mine' | 'unassigned'; set by the handler, never the caller
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
  -- requester_pattern is '%' || escaped text || '%', built in the store
  AND (sqlc.narg(requester_pattern)::text IS NULL
       OR t.guest_name  ILIKE sqlc.narg(requester_pattern)::text
       OR t.guest_email ILIKE sqlc.narg(requester_pattern)::text
       OR t.reporter_user_id IN (SELECT u.id FROM users u
                                 WHERE u.display_name ILIKE sqlc.narg(requester_pattern)::text
                                    OR u.email        ILIKE sqlc.narg(requester_pattern)::text))
  AND (sqlc.narg(created_after)::timestamptz  IS NULL OR t.created_at >= sqlc.narg(created_after)::timestamptz)
  AND (sqlc.narg(created_before)::timestamptz IS NULL OR t.created_at <  sqlc.narg(created_before)::timestamptz)
  AND (sqlc.narg(updated_after)::timestamptz  IS NULL OR t.updated_at >= sqlc.narg(updated_after)::timestamptz)
  AND (sqlc.narg(updated_before)::timestamptz IS NULL OR t.updated_at <  sqlc.narg(updated_before)::timestamptz)
  AND (cardinality(sqlc.arg(source_channels)::text[]) = 0 OR t.source_channel = ANY(sqlc.arg(source_channels)::text[]))
  AND ( …sla, section 2.3… )
  AND (
    NOT sqlc.arg(searching)::bool
    OR t.tracking_number ILIKE sqlc.arg(tracking_pattern)::text
    OR (sqlc.arg(search_query)::text <> '' AND t.search_vector @@ to_tsquery('english', sqlc.arg(search_query)::text))
    OR t.id IN (SELECT ticket_id FROM reply_hits)
  )
ORDER BY
  CASE WHEN sqlc.arg(search_query)::text <> '' THEN ts_rank(t.search_vector, to_tsquery('english', sqlc.arg(search_query)::text)) ELSE 0 END DESC,
  t.created_at DESC,
  t.id                      -- new tiebreak; the Go merge had one, the SQL did not
LIMIT sqlc.arg(result_limit) OFFSET sqlc.arg(result_offset);
```

The `ORDER BY` above is the default (a text search, no `sort`). Section 1.6
defines the others; the store picks one of them from `Filter.ResolvedSort()`.

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
- **`include_internal` is computed by the handler from the role. It is never
  a request parameter** (section 2.5).
- **One test per filter in the store (section 6)** passes only that filter and
  checks that it narrows the results. A second test passes no filters and
  checks the result against `ListAllTickets`. A typo in a `narg` name produces a
  different generated field, which fails the build. A wrong column shows up as
  a test failure.

### 2.3 SLA state in SQL

The indicator is computed in Go (`sla.StatusFor`, `colorFor`: red at ≥100%,
amber at ≥80%). A filter has to run in SQL, or pages come back short. The
filter and the sort reproduce `StatusFor` on the **current** record
(`superseded_at IS NULL`). **They follow `StatusFor`, not the breach stamps.**
`StatusFor` never reads `response_breached_at` or `resolution_breached_at`, and
a test pins that. A rule built on the stamps would disagree with the indicator
in two places: a target met in the 80-100% band stays amber (a stamp is set
only at 100%), and the stamp lags a breach by up to a sweep tick.

For each of the two targets, with elapsed time as `sla.ElapsedFrom` has it:

- a target that is **met** and has a frozen reading
  (`response_elapsed_at_met_seconds` / `resolution_elapsed_at_met_seconds`)
  uses that reading, however long ago it was met and whatever the ticket has
  done since. Recomputing it would subtract pause time that had not happened
  yet when it was met;
- any other target uses `LEAST(COALESCE(pending_since, at), at) - start -
  pause`, where `at` is the instant the target was met (a met target with no
  frozen reading, which migration 000028 could leave) or `now` while it is
  outstanding, `start` is the record's `started_at` (the ticket's creation when
  null) and `pause` is `sla_paused_seconds - paused_seconds_at_start`.

A target is red at or past 100% of its target and amber at or past 80%. The 80%
point is `target_min * 48` seconds, exactly 80%, which matches `colorFor`'s
integer `elapsed*5 >= target*4`. A ticket is in the **`breached`** bucket if
either target is red, in **`at_risk`** if neither is red and either is amber,
and in neither otherwise. A ticket with no current record is in neither.
The `sla` sort uses the same buckets: breached, at risk, the rest.

`now` is a parameter, so tests pass a fixed instant. This is the **third** copy
of the elapsed-time formula (after `sla.ElapsedFrom` and
`ListSLABreachCandidates`); all three carry a "must change together" note. A
test (section 6) checks that the SQL filter and sort and `sla.StatusFor` put
each ticket in the same bucket. It builds tickets at 79.9%, 80%, 99.9% and 100%
of target, one microsecond either side, and tickets that are pending, paused,
restarted, met on time, met late, met and then paused afterwards, and met with
no frozen reading. Because the SQL mirrors `StatusFor` exactly, there is no
known edge left to document: the migration 000028 case reads the same in both.

### 2.4 Indexes and migrations 000060 and 000061

**I checked the migration numbers again on 2026-11-10.** The newest migration
is still **`000059_custom_field_retired_options`** on `origin/v1.3.0-beta`
and on every open PR branch: #614, #633–#638, #613, #588, #577, #576 and #430.
The fix-audit branches are at 000059, the dependabot branches at 000022, the
docs branches have none, and `cf-a-design` is at 000058. No remote branch has
000060 or higher. This design takes **000060** (filter indexes, PR B) and
**000061** (reply search, PR R). If another migration merges first, the later
PR renumbers.

**000060 (PR B):**

```sql
CREATE INDEX IF NOT EXISTS tickets_updated_at_idx ON tickets (updated_at DESC);
-- requester free text: substring match needs trigrams (pg_trgm exists since 000021)
CREATE INDEX IF NOT EXISTS tickets_guest_name_trgm_idx  ON tickets USING gin (guest_name  gin_trgm_ops);
CREATE INDEX IF NOT EXISTS tickets_guest_email_trgm_idx ON tickets USING gin (guest_email gin_trgm_ops);
```

The users subquery is not indexed. It scans `users` the same way
`LookupRequesters` already does, and that table is small next to `tickets`.

Not added:
- `priority` and `source_channel`: they have few distinct values, so the
  planner would ignore an index.
- `type_id` and `item_id`: they are almost always used with `category_id`,
  which is already indexed.

PR B measures at 200k tickets, using the method from migration 000021. It
adds another index only if a measurement shows the need. The `down` migration
drops the indexes, and no data is lost.

**Generic-plan risk.** The pool uses pgx's default statement cache. Postgres
can switch a cached statement to a generic plan after 5 runs. A generic plan
for `($1 IS NULL OR col = $1)` cannot choose an index for the one filter that
is actually set. PR B measures the 6th and later runs, not only the first.
If plans get worse, the fix is one line: `plan_cache_mode=force_custom_plan`
in the pool's runtime parameters (open question 6).

### 2.5 Reply-text search (D1)

#### What it does

With `search_replies=true` and text in `q`, a ticket also matches when one
reply contains every word of the search. Words match as prefixes, the same way
the subject and description search works today. Which replies count depends
on the viewer:

| Viewer | Replies searched |
| --- | --- |
| Staff, admin, and their keys and OAuth clients | public replies and internal notes |
| Requester (session, MCP, or an OAuth client whose service user is a reporting user) and any unrecognised role | **public replies only** |

All words must appear in the **same** reply, or all in the ticket's own
subject and description. A search for `printer zebra` with "printer" in the
subject and "zebra" in a reply does not match. The query matches one reply at
a time. The same rule also stops a requester from combining a public reply
with an internal note.

The API default is `false`, so existing callers get exactly what they get
today. The page sends `true` unless the viewer clears "Search replies" (open
question 8).

#### The index: an expression index, so no table rewrite

```sql
-- 000061_reply_search.up.sql
CREATE INDEX IF NOT EXISTS ticket_replies_body_fts_idx
    ON ticket_replies USING gin (to_tsvector('english', body));
```

The earlier draft used a stored generated `tsvector` column, which is what
`tickets.search_vector` is. **This draft does not.** Adding a stored generated
column rewrites the whole `ticket_replies` table under an `ACCESS EXCLUSIVE`
lock, and the column would roughly double the space that reply text takes on
disk. An expression index gives the same `@@` lookups with no new column and
no rewrite. The cost is that ranking or highlighting a matched reply parses
its body again. Section 2.5 does that only for the rows on the page.

The query must use **exactly** `to_tsvector('english', body)`, or the
planner will not use the index. A store test checks that the reply search
plan uses `ticket_replies_body_fts_idx`.

#### Upgrade cost on a large replies table

- **What the build does.** It reads every reply and parses it into words.
  Most of the time goes to parsing, so build time grows with the total reply
  text, not with the number of rows. Replies are capped at 50,000 bytes each,
  since #443.
- **I have not measured it.** PR R measures the build on PostgreSQL 17 at
  1 million replies of realistic length, with the default
  `maintenance_work_mem` (64 MB) and with 512 MB. It records the build time and
  the index size in the PR and in the upgrade notes. I expect minutes, not
  seconds, at that size. That is a guess until it is measured.
- **The lock.** A plain `CREATE INDEX` takes a `SHARE` lock. Reads continue,
  and new replies wait until the build finishes.
- **Can the build run concurrently or online? Not inside the migration, and
  it would not help there.**
  - golang-migrate runs a migration file as one execution. I believe
    PostgreSQL treats several statements sent this way as one transaction,
    and `CREATE INDEX CONCURRENTLY` refuses to run inside a transaction. PR R
    verifies this.
  - More importantly, the server runs migrations at startup, before it
    serves requests (`cmd/server/main.go`). The help desk is not serving
    while 000061 runs, whichever kind of build is used. A concurrent build
    also reads the table twice, so it is slower.
- **The online option is to build the index before upgrading.** An operator
  with a large table can build the index while 1.2.x is still running and
  serving:

  ```sql
  CREATE INDEX CONCURRENTLY IF NOT EXISTS ticket_replies_body_fts_idx
      ON ticket_replies USING gin (to_tsvector('english', body));
  ```

  When 1.3.0 starts, 000061 finds the index and does nothing. There is one
  trap. A concurrent build that fails leaves an **invalid** index with that
  name, and `IF NOT EXISTS` would skip it without a word, so every reply
  search would scan the table. 000061 therefore starts with a block that
  drops the index if it exists and is invalid:

  ```sql
  DO $$
  BEGIN
    IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
               WHERE c.relname = 'ticket_replies_body_fts_idx' AND NOT i.indisvalid) THEN
      DROP INDEX ticket_replies_body_fts_idx;
    END IF;
  END $$;
  ```

  The `down` migration drops the index. No data is lost.
- **Ongoing cost.** Each new reply adds a GIN entry. GIN batches these in its
  pending list (`fastupdate`), so the cost per write is small.

#### Query shape

There are two steps, and both use the same `include_internal` value.

1. **Which tickets match.** This is the `reply_hits` CTE in section 2.1. It
   collects the ids of tickets with a matching reply through the GIN index.
   The main query adds `t.id IN (SELECT ticket_id FROM reply_hits)` as a
   third alternative next to the tracking-number and ticket-text matches.
   Visibility and every filter still apply to the ticket, so a reply hit
   cannot bring in a ticket the viewer cannot see.
   - **Ranking.** A ticket that matches on its own subject or description
     ranks by `ts_rank` as today. A ticket that matches only through a reply
     ranks after all of those, newest first, together with tracking-number-only
     hits. This keeps the current rule that a direct match on the ticket
     outranks anything else.
   - **What PR R measures.** The `OR … IN (subquery)` form cannot join the
     tickets indexes into one BitmapOr. If the measurement at 200k tickets and
     1M replies shows a slow plan, PR R replaces the three alternatives with a
     single `UNION` of candidate ids: tracking trigram, ticket text and
     reply text. Each part can then use its own index. PR R ships the faster
     shape and puts the numbers in the PR.

2. **The snippet**, read only for the tickets on the page (at most 200):

   ```sql
   -- name: ReplyMatchesForTickets :many
   -- One snippet per ticket: its newest matching reply the viewer may read.
   -- ts_headline parses the body again, so it runs once per ticket, not once
   -- per matching reply.
   SELECT m.ticket_id, m.id AS reply_id, m.internal, m.created_at,
          ts_headline('english', m.body, to_tsquery('english', sqlc.arg(search_query)::text),
                      'MaxFragments=1, MaxWords=24, MinWords=10, StartSel=' || chr(2) || ', StopSel=' || chr(3))
   FROM unnest(sqlc.arg(ticket_ids)::uuid[]) AS tid(id)
   CROSS JOIN LATERAL (
     SELECT r.ticket_id, r.id, r.internal, r.created_at, r.body
     FROM ticket_replies r
     WHERE r.ticket_id = tid.id
       AND to_tsvector('english', r.body) @@ to_tsquery('english', sqlc.arg(search_query)::text)
       AND (sqlc.arg(include_internal)::bool OR NOT r.internal)
     ORDER BY r.created_at DESC, r.id
     LIMIT 1
   ) m;
   ```

   - **How a match is marked.** ts_headline wraps each matched word in the
     control characters U+0002 and U+0003, not HTML tags. The UI splits on
     them and renders text nodes inside `<mark>`. It never uses
     `innerHTML`. A reply that contains these characters itself can at worst
     move a highlight. It cannot inject markup.
   - **What a snippet contains.** Snippets are raw reply text, Markdown
     symbols included, cut to 300 characters by the server.
   - **No author names** in snippets (section 1.4).

   Each ticket view on the page gets one new wrapper key:

   ```json
   "reply_match": {
     "reply_id": "uuid",
     "internal": false,
     "created_at": "2026-11-03T14:02:00Z",
     "snippet": "…paper is creasing at the \u0002fuser\u0003, so I have ordered…"
   }
   ```

   The key is absent when the ticket did not match through a reply.
   `reply_match` is added to the requester allowed list **on purpose**, as a
   wrapper key, because a requester's matches are their own public
   conversation. For a requester, `internal` is always `false`, because the
   query never reads an internal note for them.

#### How internal notes are kept out of a requester's results

This is security-critical. If an internal note could match, a requester could
use search as a yes/no test of what staff wrote about them, one word at a
time. The rules:

1. **`include_internal` comes from the actor's role only.** It is `true` for
   staff and admin and `false` for everyone else, unknown roles included. A
   `nil` actor fails closed. It is computed in the same place as
   `TicketVisibility`. **There is no request parameter that sets it.** A
   query-string `include_internal=true` is ignored like any other unknown
   parameter, and a test proves it.
2. **The match query and the snippet query both use it.** A ticket that
   matches on its subject must not show a snippet from an internal note.
   Each query has its own test for this.
3. **Matching is per reply** (above), so words in a public reply and an
   internal note cannot combine into one match.
4. **The invariant test.** It builds a fixture of tickets with public replies
   and internal notes, and runs a set of searches as a requester: ids,
   ordering and snippets. It then deletes every internal note inside the test
   transaction and runs the same searches again. **The two results must be
   identical.** This test fails for any way an internal note influences a
   requester's results, including ones nobody has thought of yet. It runs at
   the store level, and again through REST and MCP.
5. **One rule for every route.** REST, MCP `list_tickets`, and an OAuth
   client acting as a reporting user all go through the same role-to-flag
   function.

---

## 3. API

### 3.1 Handler shape

`handleListTickets` becomes five steps:

1. parse the parameters;
2. check each filter against the role and, for `requester`, the scope;
3. resolve visibility, queue and `include_internal`;
4. call `ListFiltered` and, when replies were searched, `ReplyMatchesForTickets`
   for the page;
5. call `writeTickets`.

This replaces seven branches and the 1+N merge. Parsing and the role and scope
check are one pure domain function, so REST and MCP share one definition and
cannot drift apart. The function is `ticket.ParseListFilter` together with
`Filter.AllowedFor(role, requesterSearch bool)`. The caller works out
`requesterSearch`: true for a browser session, and for an API key or OAuth
client only if it holds `requester_search:read`.

**Behaviour that stays the same:**
- the same tickets for every existing parameter combination, except the
  `reporter_id` + `assignee_group_id` pair (section 1.5);
- the same 403s and 400s, with the same messages;
- the same `limit`/`offset` rules (`pageParams`);
- a bare array as the response.

**Behaviour that changes:**
- `reporter_id` is open to staff (D2).
- A search on the `mine` queue (admin, or staff with enforcement off) is
  ranked like every other search, not newest first (open question 3).

The 12 old list and search queries lose their REST callers. They are removed
in a **separate commit**, after checking that nothing else calls them.

### 3.2 Validation

Validation runs **before** any query. A request that is refused costs no
database time.

| Case | Response |
| --- | --- |
| Not a UUID, unknown enum value, unparseable instant, bad bool | `400 bad_request`, `"invalid <param>"` (the existing wording) |
| `created_after` ≥ `created_before` (same for `updated_*`) | `400 bad_request`, `"created_after must be before created_before"` |
| More than 20 values for a repeatable key | `400 bad_request`, `"too many status_id values (max 20)"` |
| `requester` shorter than 2 or longer than 254 characters after trimming | `400 bad_request`, `"requester must be 2 to 254 characters"` |
| A filter not allowed for the role (section 1.2) | `403 forbidden`, `"<param> is not available to requesters"`; existing 403 texts unchanged |
| `requester` from a key or OAuth client without `requester_search:read` | `403 insufficient_scope`, `"this credential does not carry the requester_search:read scope"` |
| `sla` while SLA tracking is off | `400 bad_request`, `"SLA tracking is off"` (open question 7) |
| `search_replies=true` with no `q` | no effect; not an error |
| An id that names nothing | `200 []`. No existence check: it would cost a query and would show whether ids exist. |
| Unknown parameter (`include_internal` included) | ignored, as today |

### 3.3 OpenAPI and DESIGN.md

`docs/api/openapi.yaml`, `GET /api/v1/tickets`:
- Add the new parameters from 1.5. Use `style: form` and `explode: true` for
  the repeatable ones. Each description says who may use it.
- Add `reply_match` to `TicketView`, documented as present only when the
  ticket matched through a reply.
- Rewrite the paragraph that begins "Four parameters can change the listing…
  The first one present wins": filters now combine with AND.
- Add the new 400 cases, the new 403 text, and `403 insufficient_scope`.
- Add `requester_search:read` to the scope list.
- If open question 2 is approved, document `/users/lookup?id=`.

`docs/DESIGN.md`:
- Rewrite **List filters**.
- Update **Ticket Search**. The line "Reply bodies are not indexed …
  Deferred: searching reply content" becomes the rule in 2.5.
- Add the new row to **Credential Scopes**.
- Under **What a requester sees of a ticket**, add `reply_match` to the
  wrapper keys.

### 3.4 Upgrade notes (draft; PR R and PR C put it in `docs/upgrade-notes-1.3.0.md`)

> ### Ticket List Filters and Reply Search
>
> - **Filters on the ticket list.** Staff and administrators can filter by
>   requester (picked from the directory, or any part of a name or email
>   address, guests included), created and last-updated dates, status,
>   priority, category, type and item, assignee, group, how the ticket came
>   in, and SLA state (breached or at risk). Requesters can filter their own
>   tickets by status, priority, classification and created date. Filters are
>   kept in the page address, so a copied link shows the same list.
> - **The status filter and "Include closed" now work across all pages.**
>   They used to apply only to the page on screen.
> - **Search can include replies.** With "Search replies" on (the default on
>   the ticket list), a search also matches the conversation. A result found
>   this way shows a short excerpt and says whether it came from a public
>   reply or an internal note. **Requesters' searches never match internal
>   notes.**
> - **API.** `GET /api/v1/tickets` takes new optional filters. Every existing
>   request returns the same tickets as before, with two changes:
>   `reporter_id` is now open to staff (limited to the tickets they can see),
>   and searches in the default "mine" listing are ranked by relevance. The
>   reply search is `search_replies=true` and is off unless asked for. Matches
>   carry a `reply_match` object. MCP `list_tickets` takes the same filters.
> - **New scope: `requester_search:read`.** Free-text requester search
>   (`requester=`) from an API key or OAuth client needs this scope. No
>   existing key has it, and `tickets:read` does not include it. Without it the
>   answer is `403 insufficient_scope`. Filtering by `reporter_id` does not
>   need it.
> - **Migrations 000060 and 000061 add indexes only.** No data is changed.
>   000061 indexes the text of every reply, and **on an instance with many
>   replies the upgrade takes longer to start** (measured: _to be filled in by
>   PR R_). New replies wait while it runs. To avoid the wait, build the index
>   while the old version is still running:
>   `CREATE INDEX CONCURRENTLY IF NOT EXISTS ticket_replies_body_fts_idx ON
>   ticket_replies USING gin (to_tsvector('english', body));`. 1.3.0 then
>   skips the build. If that statement fails partway, 1.3.0 drops the broken
>   index and builds it again.
>
> Under *Rolling Back to 1.2.0*: the down migrations drop
> `tickets_updated_at_idx`, `tickets_guest_name_trgm_idx`,
> `tickets_guest_email_trgm_idx` and `ticket_replies_body_fts_idx`. No data is
> lost.

---

## 4. UI

### 4.1 The mock (D4)

The static mock is
`/private/tmp/claude-501/-Users-erikpitti-source-repos-publiciallc-go-help-desk/9a79e3c7-aa03-4999-bcaf-909bc777ccf4/scratchpad/searchmock/index.html`.
It uses made-up data, and its classes and colours are copied from
`TicketListPage.tsx`, `Layout.tsx` and the `ui/` components. The buttons at
the top switch between the states. There are screenshots in the same folder,
desktop at 1440×900 and phone at 390×844, full page:

| State | File |
| --- | --- |
| (a) desktop, Filters panel open, five filters set, chips, Clear all, results | `a-desktop-panel-open.png` |
| (b) desktop, panel closed, chips only (free-text requester, SLA at risk, updated range) | `b-desktop-panel-closed.png` |
| (c) phone, full-screen filter dialog | `c-phone-filter-dialog.png` |
| (d) phone, results with chips | `d-phone-results.png` |
| (e) desktop, reply-text match with snippets (public reply and internal note) | `e-desktop-reply-match.png` |

A copy of the mock and the screenshots is committed with this document, in
`docs/plans/advanced-search-mock/`, because the scratchpad is cleared after
about three days.

### 4.2 Behaviour

- **The URL holds the state.**
  - `validateSearch` on `/tickets` gains `q`, `replies`, `scope`, `status`
    (array), `priority` (array), `category`, `type`, `item`, `assignee`,
    `group`, `reporter`, `requester`, `created_from`, `created_to`,
    `updated_from`, `updated_to`, `channel` (array), `sla`, `closed` and
    `page`.
  - The `status=<id>` and `reporter=<id>` links that exist today (Dashboard,
    user page) keep working.
  - The search box writes `q` with `replace` after the 300 ms debounce, so
    typing does not fill the browser history.
  - A copied link reproduces the search.
- **Dates.** The URL holds `YYYY-MM-DD`. The page turns each date into an
  instant in the viewer's timezone. The end date is included.
- **Filters panel.**
  - On desktop it opens inline under the toolbar (state a), and each change
    applies at once.
  - Below `md` it is a full-screen Radix Dialog (state c). The changes are
    applied together with **Show results**. The admin queue switch moves into
    the dialog, and the less-used fields sit under "More filters".
  - Both layouts use one form component. There are no new dependencies.
- **Fields by role.** Requesters see Status, Priority, Category, Type, Item,
  Created and "Search replies". Staff and admin see everything. The SLA field
  is hidden while SLA tracking is off.
- **Requester field.** It reuses `RequesterPicker` (`/users/lookup`). The
  suggestions list the accounts found, then "Any requester matching
  '<text>'". Picking an account sets `reporter`. Picking the last option sets
  `requester` (free text), which also finds guests. The chip reads
  "Requester: Dana Whitfield" (state a) or "Requester contains 'whitfield'"
  (state b).
- **Chips** (states a, b, d). Each active filter is a chip with its own ×,
  followed by **Clear all**. Chips wrap and never scroll sideways: the
  screenshots measured 0 px of horizontal overflow at 390 px. They replace
  the two separate "Filtering by status" and "Filtering by client" rows.
- **Reply matches** (state e). A ticket found only through its conversation
  shows one line under the subject: an icon, "Public reply · 3 Nov" or an
  amber "Internal note · 4 Nov", and the excerpt with the matched words
  highlighted. It shows no author name. The phone card shows the same line
  under the subject.
- **The "Only <queue>" chip** (#444) is unchanged. Any filter, not only text,
  counts as "searching" for the wide search (open question 9).
- **Empty result:** "No tickets match these filters." with **Clear all**.

---

## 5. MCP

`list_tickets` gets the same filters through the same domain parse and check.
It already calls `ListFiltered` with a `ticket.Filter`.

- Arrays are JSON arrays.
- `search_replies` and `requester` are included. `requester` needs
  `requester_search:read` on a machine credential, the same as over REST.
  `include_internal` comes from the role, the same as over REST.
- `status_id` (single) is kept, and `status_ids` is added beside it, so
  existing callers still work.
- The limit stays at a default of 20 and a maximum of 100.
- `reply_match` goes through `ticketResult`, which already applies the
  requester filter.

---

## 6. Tests

Every PR writes its failing tests first (CLAUDE.md).

**Go, domain (no DB)**, `internal/domain/ticket/filter_test.go`:
- A parse table: each parameter valid, invalid, empty and repeated; the
  20-value limit; inverted ranges; `requester` length limits; the
  `include_closed` default.
- `TestListFilter_RequesterMayOnlyFilterOnAllowedKeys`. Every filter field is
  mapped to the ticket key it reads, and a requester is allowed a filter only
  if that key is on the requester allowed list. A new filter with no mapping
  fails the test.
- A role and credential table, covering each filter × {user, staff, admin,
  unknown role} × {session, key with the scope, key without it}.
- `IncludeInternalFor(role)`: `true` only for staff and admin, `false` for
  `user`, an unknown role and a nil actor.

**Go, store (real Postgres)**, `internal/database/ticketstore/ticketstore_test.go`:
- One test per filter: it narrows the results, and an absent filter does not.
- The no-filter result equals `ListAllTickets`. The `mine` queue equals the
  old 1+N merge on the same data.
- **Scope.** With `VisibilityScoped`, each filter is run against values that
  belong to an out-of-scope ticket: its reporter, its requester text, its
  assignee, group, category, dates, channel and SLA state, and a reply word
  that only it contains. The out-of-scope ticket never appears.
- **Requester free text.** Matches on the account name, the account email,
  the guest name and the guest email. Matching ignores case. `%` and `_` in
  the input are matched literally. A deleted account still matches.
- The SLA buckets agree with `sla.StatusFor` (section 2.3).
- Paging: filtered pages are full, and the `id` tiebreak keeps the order
  stable across pages.
- **Reply search: internal notes (security-critical).**
  - A ticket whose only match is an internal note is absent for
    `include_internal=false` and present for `true`.
  - A ticket that matches on its subject and also has a matching internal
    note gets no snippet for `false`.
  - Words split between a public reply and an internal note do not match for
    `false`.
  - **The invariant test (2.5, rule 4):** requester results are identical
    before and after every internal note is deleted.
- **Reply search: other.**
  - Ranking: a ticket-text match comes before a reply-only match.
  - Snippets use the control-character markers and never contain `<`
    produced by the server.
  - The plan uses `ticket_replies_body_fts_idx` (checked with `EXPLAIN`).
- **Migrations.**
  - 000060 and 000061 run up, down and up again.
  - 000061 skips a valid index that was built beforehand.
  - 000061 replaces an invalid one. The test makes it invalid by marking
    `indisvalid = false` in the test database.

**Go, handler (`httptest`)**, a new `internal/server/handler_tickets_filters_test.go`:
- Every existing parameter combination returns what it returned before. These
  tests are written against the **current** handler first, so they prove the
  rewrite changes nothing. The existing `handler_tickets_search_visible_test.go`
  and `handler_source_channel_test.go` must also pass unchanged.
- The validation table in 3.2: status codes and error JSON.
- **`requester=` by credential:**
  - a session: 200;
  - an API key with the scope: 200;
  - an API key without it: `403 insufficient_scope`;
  - an OAuth client with and without the scope: the same as an API key;
  - a requester session: 403;
  - `reporter_id` from a key without the scope: 200.
- **Scope grants.** `requester_search:read` can be granted to staff and admin
  keys, cannot be granted to an OAuth client with a reporting service user,
  and `requester_search:write` is refused.
- Staff with enforcement on: filters that name out-of-scope things return
  only in-scope tickets. A non-member `assignee_group_id` still returns 403.
- **Requesters:**
  - each staff-only filter returns 403;
  - a query-string `include_internal=true` changes nothing;
  - the reply-search invariant holds through REST;
  - the body never carries a never-listed key, and `reply_match.internal` is
    always `false`.

**Scope tests**, `internal/domain/auth`, `internal/server`:
- `ScopesForRole` includes the new scope for staff and admin.
- `TestScopesForRole_MatchTheRouter` passes with the parameter-scope list.
- Every entry on that list is checked by a handler test.

**MCP**, `internal/mcp`:
- a `buildListFilter` table for the new arguments;
- `requester` without the scope is refused;
- the requester reply-search invariant holds;
- the same requester-refusal table is run against REST and MCP.

**vitest**:
- URL to filter state and back, including old `status=`/`reporter=` links,
  and date conversion across a daylight-saving change.
- Chips clear one at a time, and Clear all clears every filter.
- A requester sees only their own fields, and the SLA field is hidden while
  tracking is off.
- `listTickets` sends repeated keys.
- The requester picker offers "Any requester matching…".
- **Snippet rendering:** a reply body containing `<img onerror>` renders as
  text, and the markers become `<mark>`.
- Paging resets when a filter changes.

**Playwright** (mocked API), `frontend/e2e/ticket-filters.spec.ts`:
- The five states of the mock, against the real page.
- A reload, or a link opened in a new page, keeps the same filters and results.
- At 390 px there is no horizontal scroll (as `mobile-layout.spec.ts`
  checks).

---

## 7. Work, order and estimate

| PR | Owns (files) | Depends on |
| --- | --- | --- |
| **A. Filter contract** | `internal/domain/ticket/filter.go`, `filter_test.go` (parse, role check, `IncludeInternalFor`) | — |
| **S. Scope** | `internal/domain/auth/scope.go` + tests, the router scope test in `internal/server`, the API-key and OAuth-client scope pickers (`frontend/src/pages/…ApiKeys…`, `…OAuthClients…` scope lists only), DESIGN.md Credential Scopes | — |
| **B. Query, store, migration 000060** | `queries/tickets.sql` (`ListTicketsFiltered` only, without the reply CTE), `internal/dbgen/*` (generated), `ticketstore.go` + tests, `000060_ticket_filter_indexes.*`, plan measurements | A |
| **R. Reply search, migration 000061** | the `reply_hits` CTE in `ListTicketsFiltered`, `ReplyMatchesForTickets`, `ticketstore.go` reply-match method + tests, `000061_reply_search.*`, `ticket/requester_view.go` (`reply_match` on the allowed list) + its key test, build-time measurement, the upgrade-notes entry (3.4) | A; merges after B (same query) |
| **C. REST handler + docs** | `internal/server/handler_tickets.go` (`handleListTickets` only), `ticket_view.go` (`reply_match` field), `handler_tickets_filters_test.go`, `docs/api/openapi.yaml` (`/tickets`, `TicketView`, scopes), DESIGN.md (Ticket Search, List filters) | A, S; merges after B and R |
| **C2. Remove old queries** (separate commit) | the 12 old queries in `tickets.sql`, the `ticketstore.go` and `ticket` service and `Store` methods with no callers | C |
| **D. Lookup by id** (if open question 2 is approved) | `handleLookupUsers`, `queries/users.sql`, test, OpenAPI `/users/lookup` | — |
| **E. MCP** | `internal/mcp/server.go` (`list_tickets`, `buildListFilter`) + tests | A, S; merges after B and R |
| **F. Frontend** | `api/tickets.ts`, `router.tsx` (`/tickets` search), `TicketListPage.tsx`, new `components/ticket/TicketFilters.tsx`, `FilterChips.tsx`, `ReplyMatch.tsx`, vitest files, `e2e/ticket-filters.spec.ts`, `e2e/mock.ts` | this contract; merges after C and D |

**Order.**
1. A first. It is small, and it fixes the struct that everyone else codes
   against.
2. S, B, R, D, E and F then run in parallel. B and R both edit
   `ListTicketsFiltered`. B merges first, and R rebases onto it: R's change
   is one CTE and one OR arm.
3. C follows B and R. E follows C.
4. F works against `e2e/mock.ts` and merges last.
5. C2 follows C.

Files overlap only in `openapi.yaml` (C and D edit different sections) and
`tickets.sql` (B, then R).

**Estimate (honest):**

| PR | Effort |
| --- | --- |
| A | 0.5 day |
| S | 1 day |
| B | 2.5 days (the SLA agreement test and the 200k-row measurement take most of it; free-text requester adds half a day) |
| R | 3 days (the invariant test, the index-build measurement at 1M replies, choosing the query shape, the upgrade notes) |
| C | 2.5 days (the "nothing changed" tests come first and are most of the work) |
| C2 | 0.5 day |
| D | 0.5 day |
| E | 0.5 day |
| F | 3.5 days (panel, dialog, URL state, chips, picker option, snippet rendering, tests) |
| **Total** | **about 14.5 working days of coding**; with 3–4 coders in parallel, **about 6–7 calendar days to merged**, including review rounds and the review gate before each merge |

**Schedule risk.** Reply search (R) is the longest item, and its
build-time measurement may change the upgrade advice. C changes the most-used
endpoint. Both land next to the pre-tag audit fix PRs. If time runs short,
the cut that still delivers the request is: keep the old handler branches
when no new parameter is present (open question 3, option b). That saves about
a day from C.

---

## 8. Open questions (each with a recommendation)

Answered so far: D1–D4 at the top. Everything below still needs a decision.
The owner asked to see the UI (section 4.1) before approving these.

1. **Name and shape of the new scope.**
   *Recommend: `requester_search:read`, a read-only resource like `audit`.*
   - It can be granted to keys owned by staff and administrators, and to OAuth
     clients whose service user is staff.
   - It is not implied by `tickets:read` or `users:read`.
   - It gates only the `requester` text filter, over REST and MCP.
   - `/users/lookup` stays session-only.

2. **What the requester chip shows after a reload or on a shared link.** The
   URL holds an account id, and staff cannot turn an id into a name today.
   *Recommend: add `GET /users/lookup?id=<uuid>`.* It would be session-only,
   with the same exclusions as the lookup, and fall back to "Requester ab12…"
   if the lookup fails. Do not put the name in the URL: a shared link could
   then show "Dana" while it filters for someone else. Free-text chips need no
   lookup.

3. **Should every list request go through the one query?** This makes a
   search on the `mine` queue ranked by relevance instead of newest first.
   - *Recommend: (a) yes.* It removes seven branches and the 1+N merge, and
     matches DESIGN.md's "ranked by relevance".
   - (b) Keep the old branches when no new parameter is present. That
     changes less before the tag, but leaves two paths that can drift apart.

4. **The upgrade advice for the reply index.**
   *Recommend:* build it in the migration with the invalid-index guard, and
   document the pre-build statement for large instances. Do not set
   `maintenance_work_mem` in the migration, because small hosts could run out
   of memory. Decide again once PR R has numbers. If the build takes more than
   a few minutes at 1M replies, say so in **bold** in the upgrade notes.

5. **Should masking turn off name matching?** Should
   `audit_mask_requester_names=everywhere` also turn off `requester` text
   matching?
   *Recommend: no.* The setting is display-only for the audit views, and
   DESIGN.md says it is not an access control. Search adds no name to any
   response or audit entry. Turning off name matching would give the setting
   a second meaning.

6. **What if the measurement shows bad cached plans?**
   *Recommend: measure first in PR B.* If plans get worse, set
   `plan_cache_mode=force_custom_plan` on the pool. It is one line, and it
   applies to every query.

7. **What should `sla=` do while SLA tracking is off?**
   *Recommend: `400 "SLA tracking is off"`, with the field hidden in the UI.*
   An empty list would look like "nothing is breached".

8. **Should the page search replies by default?**
   *Recommend: yes, on the page (the "Search replies" box is ticked); off by
   default in the API, so existing callers are unaffected.* Searching replies
   is what people expect from a help desk search. The checkbox is there for
   someone who wants subject and description only.

9. **Should any filter widen a staff search to "all tickets you can see", as
   text in the box does?**
   *Recommend: yes.* It is the same rule as #444, and the "Only <queue>" chip
   narrows it again.

10. **Which timezone should dates in a shared link use?**
    *Recommend: the viewer's timezone, and accept the shift.* There is no
    per-user or instance timezone setting.

11. **Should tags, custom fields or sort order be filters too?**
    *Recommend: not in 1.3.0.* The `Filter` struct and the query can take
    them later without a redesign.
