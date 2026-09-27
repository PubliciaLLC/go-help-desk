# CLAUDE.md — Go Help Desk

## Source of Truth

**docs/DESIGN.md** is the specification. Read it before implementing any feature. If a requirement is ambiguous, ask — don't guess and don't invent. This file governs *how* we build, not *what*.

---

## Workflow: Feature Implementation

Every feature follows this order. No exceptions.

1. **Understand the requirement.** Restate it before writing a single line. If it doesn't fit in one sentence, the scope is too large — break it down.
2. **Write the test first.** A failing test defines what "done" means. No implementation without a test.
3. **Implement the minimum code to pass the test.** No more. Speculative abstractions are bugs waiting to happen.
4. **Refactor if the code is ugly.** But only after the tests are green.

---

## Engineering Principles

### Data structures first

Before writing a function, name the data. What does it hold? Who owns it? Who mutates it? A bad data structure poisons every function that touches it. Get the struct right and the functions write themselves.

### Eliminate special cases — don't patch them

If you're writing an `if` to handle an edge case, ask whether a different data structure makes the edge case impossible. Three conditional branches usually means the wrong abstraction. Redesign before you branch.

### Simplest solution that solves the actual problem

Not the cleanest generalization. Not the most extensible framework. The simplest thing that works for the problem we have right now. We will refactor when the next real requirement arrives.

### Recorded architecture decisions

Some shapes in this codebase look unfinished and are not. Before "fixing" one,
check whether it is listed here.

- **`server.Server` holds concrete `*Service` pointers, not interfaces.**
  Deliberate; see the comment above `ProtectMCP` in `internal/server/server.go`
  for the measurements. Short version: the handlers touch 140 service methods,
  so inverting means ~280 method signatures, and the test-speed problem it was
  meant to solve was bcrypt cost, not coupling. The three fields that *are*
  interfaces are narrow contracts crossing a package boundary, which is a
  different thing.
- **`ticket.Service.Close` does not call `CanTransitionStatus`.** The
  auto-close scheduler has no actor; authorisation belongs to the caller. A
  test pins this so it does not get "fixed".
- **`ticket.Atomic` takes both a `Store` and an `audit.Store`.** An audit entry
  committed apart from the change it describes is not an audit trail.
- **`user.Service.writeAuditEntry` (ResetMFA, AdminSetPassword) is NOT
  transactional with the write it describes, on purpose — the one exception to
  the `ticket.Atomic` rule above.** The mutation commits first; the audit
  write is a separate, best-effort step, logged and swallowed on failure
  rather than rolled back or retried (see the doc comment on
  `writeAuditEntry`, `internal/domain/user/service.go`). This is deliberate,
  not an oversight that inherited the wrong shape: per `audit.Store`'s own
  documented contract ("log and continue rather than blocking the caller"),
  and per #306's own framing, an audit entry here does not gate anything —
  whoever can clear a factor or reset a password already holds the
  capability, so losing one entry to a crash between the two writes is a
  visibility gap, not a security control, and failing the actual reset over a
  logging hiccup would be the wrong trade. `ticket.Atomic` exists because a
  ticket's audit trail IS relied on as a record of what changed and when;
  this path is not held to the same bar. Do not "fix" this into
  `ticket.Atomic`'s shape without re-reading #306 first.
- **Attachments are download-only; there is no previewer.** Not an oversight
  and not a backlog item. Rendering attachment content on the help desk origin
  makes every uploaded file a candidate for stored XSS against the staff
  sessions that live there. The case that bites is PDF: browsers open it in a
  viewer that runs JavaScript. Three things hold it, each with a test: the
  blob content type, `Content-Disposition: attachment`, and the download
  route refusing any request whose `Sec-Fetch-Dest` says it is going to be
  rendered (with `Vary` on it, or the cache answers the second request) — the
  headers are ignored for a subresource, so `<img src>` would render an
  attachment without that third check. It does not cover script fetching the
  bytes and rendering them itself; nothing on the server can. See docs/DESIGN.md and #165 before adding a
  thumbnail, a lightbox or an inline PDF view.

- **The last-administrator guard does not cover credentials, and that is
  correct only while a password is always a way in.** The three
  `...UnlessLastAdmin` statements in `queries/users.sql` refuse to disable,
  demote or delete the last active administrator, because setup does not
  reopen. Removing that administrator's last way to authenticate is the same
  permanent mistake, and the guard says nothing about it — deliberately.

  It is safe today because of a mechanism, not a coincidence: MFA enrolment
  lives outside `RequireMFA` in `meRouter`, so an administrator with no working
  factor still signs in with their password, reaches enrolment and recovers
  alone. `TestSoleAdministrator_CanSelfRecoverWithNoSecondFactor` fails if those
  routes are ever moved behind the gate.

  **This stops being true the moment passwordless sign-in exists** (passkeys,
  see docs/DESIGN.md → Authentication → Passkeys). When the password is no
  longer a way in, self-recovery stops working and an administrator whose last
  credential is removed is locked out for good. The guard grows its fourth case
  in the change that introduces passwordless — not afterwards, and not as a
  follow-up issue. This entry is the opposite shape to the rest of this list:
  the others say "looks unfinished, is not". This one says "looks finished, is
  conditional, and here is the condition".

- **CIRCL hashlookup's `KnownMalicious` field is read by nothing, on purpose.**
  It is a field named `KnownMalicious`, sitting in a response we parse, holding
  the string `"malshare.com"`, and wiring it to the `detected` verdict would be
  a one-line change that reports jQuery to staff as malware. Measured against
  the live service, the files carrying it include `jquery-1.12.4.min.js`,
  `fontawesome-webfont.woff2`, a 1x1 spacer GIF and the hash of the two bytes
  `1\n`. MalShare's corpus is everything ever submitted to it, benign assets
  carved out of malware samples included, so the field means "these bytes have
  appeared in MalShare" and nothing else. It is left undecoded rather than
  decoded and ignored, so nothing can start branching on it by accident.
  `hashlookup:trust` is not a verdict either: it starts at 50, adds 5 per
  parent archive, subtracts 20 for `KnownMalicious` and caps at 100, so EICAR
  scores 100 — a breadth counter, not an opinion. Both are documented at the
  `clRecord` type in `internal/reputation/circl.go`, and tests pin that neither
  changes a verdict.

### No breaking changes

When extending a feature, existing behavior must not change. Tests that pass before your change must still pass after it. If something must be removed, deprecate with an explicit comment explaining why, then remove in a separate commit.

---

## Go Conventions

### Project layout

```
backend/
  cmd/server/         # main package, wires everything together
  internal/
    config/           # config loading (env vars, file)
    database/         # DB connection, migrations
    dbgen/            # sqlc-generated query code (do not hand-edit)
    domain/           # core business logic — no HTTP, no DB, no framework
    mcp/              # MCP server layer
    middleware/       # HTTP middleware
    server/           # HTTP handlers and routing
  queries/            # raw SQL for sqlc
```

`internal/domain` is the heart of the system. It must not import `database`, `server`, or any infrastructure package. Dependencies point inward.

### Error handling

- Return errors; never swallow them silently.
- Wrap errors with context: `fmt.Errorf("creating ticket: %w", err)`.
- Only log at the boundary (handler or main). Domain code returns errors; it does not log them.

### Naming

- Short, precise names. A variable that lives for 3 lines doesn't need a paragraph.
- Exported types are for things that genuinely cross package boundaries.
- Unexported functions are the default.

### No magic

No `init()` functions with side effects. No global state. Dependencies are passed explicitly. If wiring is verbose, that's fine — it's honest.

---

## Testing Conventions

### Test location

Tests live alongside the code they test (`foo_test.go` next to `foo.go`). Integration tests (DB, HTTP) go in `internal/server/server_test.go` or `internal/database/database_test.go`.

### Test style

Use Go's stdlib `testing` package and table-driven tests. Example structure:

```go
func TestCreateTicket(t *testing.T) {
    cases := []struct {
        name    string
        input   CreateTicketInput
        want    Ticket
        wantErr bool
    }{
        {
            name:  "valid ticket",
            input: CreateTicketInput{Subject: "Printer broken", CategoryID: 1},
            want:  Ticket{Subject: "Printer broken", Status: StatusNew},
        },
        {
            name:    "missing subject",
            input:   CreateTicketInput{CategoryID: 1},
            wantErr: true,
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            // ...
        })
    }
}
```

### What to test

- **Domain logic** — always. Pure functions, validation, state transitions.
- **HTTP handlers** — yes, with `httptest`. Test the contract (status codes, JSON shape), not implementation details.
- **Database queries** — integration tests against a real Postgres instance. Do not mock the DB. Mocks hide the bugs that matter most.

### Database tests

Use a test database. Each test that writes data runs in a transaction that is rolled back at the end. No test should depend on state left by another.

---

## Running Tests

### Unit tests (no DB required)

```sh
cd backend
go test ./internal/domain/... ./internal/config/... ./internal/middleware/... ./internal/server/notify/...
```

### Integration tests (require Postgres)

Integration tests are skipped automatically when `TEST_DATABASE_URL` is unset.

```sh
# Against a throwaway database (recommended)
#
# Starts an ephemeral Postgres on 127.0.0.1:5433, runs the suite against it,
# and leaves nothing behind. Never points at the development database.
./scripts/test-db.sh test

# Or against the development stack's database, which the suite will migrate
cd backend
TEST_DATABASE_URL="postgres://helpdesk:helpdesk@localhost:5432/helpdesk?sslmode=disable" go test ./...
```

### Running the full app

```sh
docker-compose -f docker/docker-compose.yml up --build
```

Serves on **http://localhost:8080** — both API and React SPA from the same port.

### First-run setup

On a fresh database, navigate to `/setup`. The setup route is only accessible when no users exist; it creates the first admin account. Once complete it redirects to `/login` and the route is permanently blocked.

API endpoints (no auth required, 409 once users exist):
- `GET  /api/v1/setup/status` → `{ "needed": true|false }`
- `POST /api/v1/setup`        → `{ "email", "display_name", "password" }`

---

## Scope Guards

Before adding anything, ask:

- Is this in DESIGN.md? If not, stop and discuss.
- Does v1 need it, or is it explicitly deferred to v2/v3/v4?
- Can the existing data model support it without a new abstraction?

**Version discipline:** v1 scope is defined in DESIGN.md. Do not implement v2+ features ahead of schedule, even if it seems "easy to add now." The cost is always higher than it looks — it lands in the test suite, the data model, and every future reader's mental model.
