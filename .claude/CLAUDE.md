# CLAUDE.md — Open Help Desk

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
# Via Docker Compose (recommended)
docker-compose -f docker/docker-compose.yml --profile test run --rm test

# From the host (requires port 5432 exposed — it is by default)
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
