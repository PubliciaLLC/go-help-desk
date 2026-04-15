# Contributing to Go Help Desk

All contributions are welcome — bug reports, documentation improvements, and code changes.

## Before writing code

1. **Read [docs/DESIGN.md](docs/DESIGN.md).** It is the specification. Every feature and behavior described there is intentional. If something is ambiguous or you think the design is wrong, open an issue to discuss it — don't work around it.
2. **Check the issue tracker.** Your bug may already be filed, or your feature may already be planned or deliberately excluded.
3. **Open an issue for non-trivial changes** before submitting a PR. This prevents wasted effort if the direction isn't right.
4. **Tests are required.** Write a failing test that defines "done" before implementing anything. No untested code will be merged.

Go Help Desk is licensed under the [GNU Affero General Public License v3.0](LICENSE). By contributing you agree that your contributions will be released under the same license.

---

## Development environment

### Requirements

- Go 1.24+
- Node.js 24+ and npm
- PostgreSQL 17+ (local or Docker)
- [sqlc](https://sqlc.dev) (for schema changes)

### Backend

```sh
cd backend
go mod download
```

Start the backend server:

```sh
DATABASE_URL="postgres://localhost:5432/helpdesk_dev?sslmode=disable" \
BASE_URL="http://localhost:8080" \
SESSION_SECRET="dev-session-secret-change-me" \
JWT_SECRET="dev-jwt-secret-change-me" \
APP_ENV=development \
go run ./cmd/server
```

### Frontend

```sh
cd frontend
npm ci
npm run dev   # starts at http://localhost:5173
```

The Vite dev server proxies `/api` and `/mcp` to `:8080`.

---

## Running tests

### Unit tests (no DB required)

```sh
cd backend
go test ./internal/domain/... ./internal/config/... ./internal/middleware/... ./internal/server/notify/...
```

### Integration tests

```sh
# Via Docker Compose (recommended)
docker-compose -f docker/docker-compose.yml --profile test run --rm test

# From the host (port 5432 is exposed by docker-compose)
TEST_DATABASE_URL="postgres://helpdesk:helpdesk@localhost:5432/helpdesk?sslmode=disable" go test ./...
```

If `TEST_DATABASE_URL` is not set, integration tests are skipped (not failed).

### Full check

```sh
cd backend
go build ./...
go vet ./...
go test ./internal/domain/... ./internal/config/... ./internal/middleware/... ./internal/server/notify/...
TEST_DATABASE_URL="..." go test ./... -race -count=1
```

---

## Schema changes

1. Add a numbered migration pair to `backend/internal/database/migrations/` (e.g. `000010_foo.up.sql` / `000010_foo.down.sql`). Always write both directions.
2. Add queries to `backend/queries/`.
3. Run `sqlc generate` from `backend/`. Review the diff in `backend/internal/dbgen/` — never hand-edit that directory.
4. Add store methods in `backend/internal/database/<feature>store/` that call the generated functions.
5. Add integration tests before using the new code in domain services or HTTP handlers.

---

## PR process

1. **Fork and branch** — create a feature branch from `main`. Name it descriptively: `feat/webhook-retries`, `fix/sla-timer-pause`.
2. **Keep the diff small** — one logical change per PR. Split refactoring from feature work.
3. **Tests green** — run the full suite locally before opening the PR.
4. **Write a clear description** — explain what the change does and why. Link to the relevant issue.

### What gets merged quickly

- Bug fixes with a regression test
- Documentation improvements
- Features explicitly called out in DESIGN.md for the current version
- Small, well-scoped refactors with clear motivation

### What gets rejected

- Features not in DESIGN.md without prior discussion
- Code without tests
- Breaking changes to existing API behavior without a migration path
- Speculative abstractions

For code conventions, see [docs/code-conventions.md](docs/code-conventions.md).
