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

- Go 1.27+
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
SESSION_SECRET="dev-session-secret-change-me-32c" \
JWT_SECRET="dev-jwt-secret-change-me" \
go run ./cmd/server
```

### Frontend

```sh
cd frontend
npm ci
npm run dev   # starts at http://localhost:5173
```

The Vite dev server proxies `/api` and `/mcp` to `:8080`.

### Optional: local email

To test email notifications locally without an SMTP server, use [Mailpit](https://mailpit.axllent.org):

```sh
docker run -d -p 1025:1025 -p 8025:8025 axllent/mailpit
```

Then add to your backend environment:

```sh
SMTP_HOST=localhost
SMTP_PORT=1025
SMTP_FROM=dev@localhost
```

View captured emails at `http://localhost:8025`.

### Building and the version string

To override the version string at build time:

```sh
go build -ldflags "-X github.com/publiciallc/go-help-desk/backend/internal/version.Version=1.0.0" ./cmd/server
```

The Docker build path is `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`, run from the `docker/` directory. It needs Docker Buildx. See [Building from source](https://gohelpdesk.org/docs/getting-started#build-from-source).

---

## Running tests

### Integration tests (Postgres)

Integration tests skip themselves when `TEST_DATABASE_URL` is unset, so the unit tests (below) need nothing installed. To run the full suite you need a throwaway Postgres:

```sh
./scripts/test-db.sh once            # start db, run everything, tear it all down
./scripts/test-db.sh test ./internal/server/ -run OIDC   # iterate on one package
./scripts/test-db.sh up              # leave it running while you work
./scripts/test-db.sh down            # stop it
```

The database is ephemeral: it lives in a tmpfs, listens on **5433** so it can never be confused with the dev stack on 5432, and runs with `fsync=off`.

That default is **one shared database**: two runs at once (two worktrees, or two agents) tear each other's database down, because `once` ends by removing it. To run several, give each its own instance and it gets its own free port:

```sh
GHD_TEST_INSTANCE=pr368 ./scripts/test-db.sh once ./...
```

Use one name per worktree. `GHD_TEST_PORT` pins the port if you need it, and only with an instance name: on the shared database it would recreate it under anyone using it, so the script refuses. Nothing persists between runs and nothing needs seeding — `testutil.NewDB` applies the migrations on first connect and each test rolls back its own transaction.

The suite records a checksum of every migration it applies. If it stops with "the test database was migrated by different migration files than this tree's", the database was migrated by another branch that numbered a migration the same as yours, or a migration you edited after it was applied. golang-migrate tracks only the number, so it would otherwise call the database up to date and your tests would fail on a missing table. Recreate the test database (`./scripts/test-db.sh down`, with the same `GHD_TEST_INSTANCE` if you set one). Pointing `TEST_DATABASE_URL` at the dev database adds one table, `testutil_migration_checksums`, which the server ignores. If two open branches both add the next migration number, the second one to merge renumbers.

On macOS the script will start [colima](https://colima.run) if no container runtime is responding, and stop it again on teardown, so no VM idles between runs:

```sh
brew install colima
```

Do **not** run `brew services start colima` — that restarts the VM at every login, which is what this setup exists to avoid.

### Unit tests (no DB required)

```sh
cd backend
go test ./internal/domain/... ./internal/config/... ./internal/middleware/... ./internal/server/notify/...
```

### Integration tests

```sh
# Against a throwaway database (recommended)
#
# Starts an ephemeral Postgres on 127.0.0.1:5433, runs the suite against it,
# and leaves nothing behind. Never points at the development database.
./scripts/test-db.sh test

# Several at once (worktrees, parallel agents): name an instance, and each
# gets its own database on its own free port. Without a name they share one,
# and the first run to finish tears it down under the others.
GHD_TEST_INSTANCE=my-branch ./scripts/test-db.sh once

# Or against the development stack's database, which the suite will migrate
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

### Test-coverage guard

CI fails a pull request that adds or changes Go code without tests. Run the same check locally before you push:

```sh
scripts/check-test-coverage.sh            # defaults to origin/main...HEAD
scripts/check-test-coverage.sh main HEAD  # or name the refs explicitly
```

It reports two levels:

- **Blocking** — a change lands in a package with no tests at all, or adds a new `.go` file with no test accompanying it.
- **Warning** — a package with existing tests was changed but no test changed with it. Not fatal, but if the change alters behaviour, a test should move too.

Generated and wiring-only paths are exempt; the list is at the top of the script. Note that the guard only checks a test *exists* — it cannot tell a real assertion from a line that makes a fake satisfy a new interface. It is a floor, not a substitute for reading the tests.

### Frontend tests

```sh
cd frontend
npm test          # unit tests (vitest)
npm run test:e2e  # end-to-end tests (Playwright)
```

---

## Schema changes

1. Add a numbered migration pair to `backend/internal/database/migrations/` (e.g. `000010_foo.up.sql` / `000010_foo.down.sql`). Always write both directions.
2. Add queries to `backend/queries/`.
3. Run `sqlc generate` from `backend/`. Review the diff in `backend/internal/dbgen/` — never hand-edit that directory.
4. Add store methods in `backend/internal/database/<feature>store/` that call the generated functions.
5. Add integration tests before using the new code in domain services or HTTP handlers.

---

## Code conventions

For code conventions, see [docs/code-conventions.md](docs/code-conventions.md).

---

## PR process

1. **Fork and branch** — create a feature branch from `main`. Name it descriptively: `feat/webhook-retries`, `fix/sla-timer-pause`.
2. **Keep the diff small** — one logical change per PR. Split refactoring from feature work.
3. **Tests green** — run the full test suite locally before opening the PR.
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
