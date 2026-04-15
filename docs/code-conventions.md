# Code Conventions

## Project layout

```
backend/
  cmd/server/         # main package: wires everything, starts the server
  internal/
    config/           # env-var config loading
    database/         # pgxpool connection, migrations, store implementations
    dbgen/            # sqlc-generated code (never hand-edit)
    domain/           # business logic (no HTTP, no DB imports)
    mcp/              # MCP server layer
    middleware/       # HTTP middleware (session auth, API key auth)
    server/           # HTTP handlers and routing
    testutil/         # shared test helpers
    ui/               # embedded React SPA (go:embed)
  queries/            # raw SQL for sqlc
```

The `domain` packages are the heart of the system. They must not import `database`, `server`, or any infrastructure package. All dependencies flow inward — domain code calls store interfaces, not store implementations.

## Error handling

- Always return errors. Never silently swallow them.
- Wrap errors with context: `fmt.Errorf("creating ticket: %w", err)`.
- Log only at the boundary (HTTP handler or `main`). Domain code returns errors — it does not log them.
- Map known domain errors to HTTP status codes in the handler or `server/respond.go`. Unknown errors become 500.

## No global state

No `init()` functions with side effects. No package-level variables that change at runtime. All dependencies — stores, services, configs — are passed explicitly through constructors and function parameters.

## Testing

- Use Go's stdlib `testing` package. No third-party assertion libraries.
- Table-driven tests for domain logic and HTTP handlers.
- Integration tests use `testutil.TxQueries` — rolled back automatically, no pre-existing data dependency.
- **Never mock the database.** Real queries catch real bugs.
- Test the contract (status codes, JSON shape, domain behavior), not the implementation.

## Naming

- Short, precise variable names. A variable that lives for 3 lines doesn't need a paragraph.
- Exported types are for things that genuinely cross package boundaries. Default to unexported.

## Scope discipline

Before adding anything, ask: Is it in DESIGN.md? If not, it's out of scope for the current version. Do not implement v2+ features ahead of schedule, even if it "seems easy to add now." The cost is always higher than it looks — it lands in the test suite, the data model, and every future reader's mental model.

## Engineering principles

### Data structures first

Before writing a function, name the data. What does it hold? Who owns it? Who mutates it? A bad data structure poisons every function that touches it. Get the struct right and the functions write themselves.

### Eliminate special cases — don't patch them

If you're writing an `if` to handle an edge case, ask whether a different data structure makes the edge case impossible. Three conditional branches usually means the wrong abstraction. Redesign before you branch.

### Simplest solution that solves the actual problem

Not the cleanest generalization. Not the most extensible framework. The simplest thing that works for the problem we have right now. We will refactor when the next real requirement arrives.

### No breaking changes

When extending a feature, existing behavior must not change. Tests that pass before your change must still pass after it. If something must be removed, deprecate with an explicit comment explaining why, then remove in a separate commit.
