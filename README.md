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
- Role-based access (admin / staff / end-user)
- Group-based staff scope tied to CTI categories
- Linked tickets (related, parent/child, duplicate, caused-by)
- Email and webhook notifications
- Optional SLA tracking
- REST API with API key auth
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

Open `http://localhost:8080`. The first account registered is automatically an admin.

## Configuration

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
| `SAML_ENABLED` | | `false` | Enable SAML 2.0 |
| `SAML_METADATA_URL` | | — | IdP metadata URL |
| `MFA_ENABLED` | | `false` | Enable TOTP MFA |
| `SLA_ENABLED` | | `false` | Enable SLA tracking |
| `GUEST_SUBMISSION_ENABLED` | | `false` | Allow unauthenticated ticket submission |
| `ATTACHMENT_DIR` | | `/data/attachments` | Attachment storage path |

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
cd backend
go test ./internal/domain/...                                       # unit (no DB)
TEST_DATABASE_URL=postgres://... go test ./internal/...            # integration
```

Schema changes: edit `queries/*.sql`, add a migration, run `sqlc generate`. Never hand-edit `internal/dbgen/`.

## License

[GNU Affero General Public License v3.0](LICENSE). Modifications — including hosting as a service — must be released under the same license.
