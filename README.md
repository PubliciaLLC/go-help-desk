# Open Help Desk

A self-hosted help desk for teams that want full control. Single binary, batteries included.

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](LICENSE)

---

## What it is

Open Help Desk is an open-source ticket management system. Staff submit and track support requests. Support teams triage, respond, and resolve them. Everything runs on your infrastructure.

- **No cloud required.** PostgreSQL + a single Go binary.
- **No SaaS lock-in.** Your data stays where you put it.
- **Deploys in one command.**

## Features

- CTI ticket classification (Category → Type → Item)
- Local accounts, TOTP MFA, and SAML 2.0 SSO
- Role-based access (Admin / Staff / User)
- Groups — named pools of staff; tickets can be assigned to a group and any member can act on them
- Group scoping tied to CTI categories (a group is only assigned tickets it owns)
- Linked tickets (related, parent/child, duplicate, caused-by)
- Email and webhook notifications
- Optional SLA tracking
- Configurable branding — site name and logo upload (PNG, SVG, JPG, GIF; auto-scaled to 320 × 64 px) via the admin UI
- REST API with API key and OAuth2 client-credential auth
- MCP server for AI assistant integration
- WASM plugin system (sandboxed)
- Guest ticket submission (optional)

## Quick start

```sh
git clone https://github.com/open-help-desk/open-help-desk
cd open-help-desk/docker
cp .env.example .env   # set SESSION_SECRET, JWT_SECRET, BASE_URL
docker compose up -d
```

Open `http://localhost:8080`. On a fresh database the app redirects to `/setup`, where you create the first admin account. The setup route is permanently disabled once any user exists.

## Configuration

Environment variables control infrastructure; feature flags (SAML, MFA, SLA, guest submission) and branding are managed through the **Admin → Settings** UI and stored in the database.

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | `postgres://user:pass@host/db?sslmode=disable` |
| `BASE_URL` | yes | — | Public URL (e.g. `https://helpdesk.example.com`) |
| `SESSION_SECRET` | yes | — | Random secret ≥ 32 chars |
| `JWT_SECRET` | yes | — | Random secret ≥ 32 chars |
| `HTTP_PORT` | | `8080` | Listen port |
| `SMTP_HOST` | | — | Enables email notifications when set |
| `SMTP_PORT` | | `587` | |
| `SMTP_USER` | | — | |
| `SMTP_PASSWORD` | | — | |
| `SMTP_FROM` | | — | |
| `ATTACHMENT_DIR` | | `/data/attachments` | Attachment storage path |
| `APP_ENV` | | `production` | Set to `development` for verbose logging |
| `LOG_LEVEL` | | `info` | `debug`, `info`, `warn`, `error` |

> **Note:** SAML, MFA, SLA, and guest-submission are toggled in the Admin UI — not environment variables. Environment variables that existed for these in older versions have been removed.

## API

The REST API is documented informally by the handler source at `backend/internal/server/`. Key endpoints:

| Endpoint | Auth | Description |
|---|---|---|
| `GET /api/v1/site` | none | Public branding info and app version |
| `GET /api/v1/logo` | none | Serve the uploaded logo file |
| `POST /api/v1/admin/settings/logo` | admin | Upload a logo (multipart, field `logo`) |
| `DELETE /api/v1/admin/settings/logo` | admin | Remove the logo |
| `GET /api/v1/setup/status` | none | Whether first-run setup is needed |
| `POST /api/v1/setup` | none (once) | Create the first admin account |
| `POST /api/v1/auth/local/login` | none | Session login |
| `GET/POST /api/v1/tickets` | session / API key | List or create tickets |
| `GET/PATCH /api/v1/tickets/{id}` | session / API key | Get or update a ticket |
| `GET /api/v1/groups` | staff / admin | List groups (for ticket assignment) |
| `GET/POST /api/v1/admin/groups` | admin | Manage groups |
| `GET/POST /api/v1/admin/groups/{id}/members` | admin | Manage group membership |

OAuth2 client credentials (`POST /api/v1/auth/oauth/token`) produce short-lived JWTs for machine-to-machine access.

## Development

Requires Go 1.24+, Node 24+, PostgreSQL 17+.

```sh
# backend
cd backend && go mod download
go run ./cmd/server

# frontend (in a separate terminal)
cd frontend && npm ci && npm run dev
```

The Vite dev server proxies `/api` and `/mcp` to `:8080`.

Tests:

```sh
# Unit tests (no DB required)
cd backend
go test ./internal/domain/... ./internal/config/... ./internal/middleware/... ./internal/server/notify/...

# Integration tests via Docker Compose
docker-compose -f docker/docker-compose.yml --profile test run --rm test

# Integration tests from the host (port 5432 is exposed)
TEST_DATABASE_URL=postgres://helpdesk:helpdesk@localhost:5432/helpdesk?sslmode=disable go test ./...
```

Schema changes: edit `queries/*.sql`, add a migration under `internal/database/migrations/`, run `sqlc generate`. Never hand-edit `internal/dbgen/`.

To override the version string at build time:

```sh
go build -ldflags "-X github.com/open-help-desk/open-help-desk/backend/internal/version.Version=1.0.0" ./cmd/server
```

## License

[GNU Affero General Public License v3.0](LICENSE). Modifications — including hosting as a service — must be released under the same license.
