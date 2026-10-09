# Go Help Desk

A self-hosted help desk for teams that want full control. Single binary, batteries included.

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](LICENSE)

![Dashboard](https://raw.githubusercontent.com/PubliciaLLC/go-help-desk/gh-pages/screenshots/02-dashboard.png)

**Documentation: https://gohelpdesk.org.** Install, configuration, upgrade notes, the admin guide and the API reference live there.

---

## What it is

Go Help Desk is an open-source ticket management system. Staff submit and track support requests. Support teams triage, respond, and resolve them. Everything runs on your infrastructure.

- **No cloud required.** PostgreSQL + a single Go binary.
- **No SaaS lock-in.** Your data stays where you put it.
- **Pulls and runs in one command.**

## Features

- CTI classification (Category → Type → Item), custom statuses, linked tickets, and follow-up tickets for closed ones
- Local, SAML 2.0 and OIDC sign-in; TOTP or passkey as a second factor
- Roles (Admin, Staff, User), groups, and group scoping by CTI category
- Guest submission and self-service signup, both off by default
- Custom fields, tags, canned responses, and full-text search
- Optional SLA tracking and auto-close
- Readable audit log with opt-in retention
- Email and webhook notifications (Slack, Teams, Discord and JIRA formats), queued with retries
- Attachments: content detection, optional ClamAV scanning and reputation lookups, download only
- REST API with OpenAPI 3.1 and an MCP server for AI assistant integration

Full list and details: https://gohelpdesk.org

## Quick start

```sh
git clone https://github.com/PubliciaLLC/go-help-desk
cd go-help-desk/docker
cp .env.example .env   # set SESSION_SECRET, JWT_SECRET, BASE_URL
docker compose up -d
```

This pulls a published image. Nothing is compiled on your machine, and one
image name covers both Intel and ARM — including Apple Silicon — because the
Docker client picks the right architecture for you.

Open `http://localhost:8080`. On a fresh database the app redirects to `/setup`, where you create the first admin account. The setup route is permanently disabled once any user exists.

These are covered on the site:

- Building from source instead (needs Docker Buildx): https://gohelpdesk.org/docs/getting-started#build-from-source
- Pinning a version (`GHD_VERSION`): https://gohelpdesk.org/docs/getting-started#pin-version
- Virus scanning (ClamAV, opt-in): https://gohelpdesk.org/docs/getting-started#config-scanning

## Documentation

- [Getting started and configuration](https://gohelpdesk.org/docs/getting-started#configuration)
- [Upgrading to 1.3.0](https://gohelpdesk.org/docs/upgrading-1.3.0)
- [Upgrading to 1.2.0](https://gohelpdesk.org/docs/upgrading-1.2.0)
- [Admin guide](https://gohelpdesk.org/docs/admin-guide)
- [REST API guide](https://gohelpdesk.org/docs/api) and [API reference](https://gohelpdesk.org/docs/api-reference)
- [MCP server](https://gohelpdesk.org/docs/mcp)
- [Security advisories and reporting](https://gohelpdesk.org/docs/security), and [SECURITY.md](SECURITY.md)

## Upgrading

Read the full notes before upgrading: [1.3.0](https://gohelpdesk.org/docs/upgrading-1.3.0) · [1.2.0](https://gohelpdesk.org/docs/upgrading-1.2.0).

**1.3.0**

- ClamAV is opt-in in Docker Compose. An install that relied on it stops scanning unless `docker/.env` sets `CLAMAV_ADDR` and `COMPOSE_PROFILES=antivirus` (#297). [Details](https://gohelpdesk.org/docs/upgrading-1.3.0#clamav)
- The audit log is readable and kept forever by default. Retention is opt-in (#129). [Details](https://gohelpdesk.org/docs/upgrading-1.3.0#audit)
- Signup: the name and password are chosen at email verification, and `POST /api/v1/auth/verify-email` now requires them (#360, #374). [Details](https://gohelpdesk.org/docs/upgrading-1.3.0#signup)
- `guest_submission_enabled` needs a signed-in administrator (#177). [Details](https://gohelpdesk.org/docs/upgrading-1.3.0#guest-setting)
- Reporters can attach files to replies (#175). [Details](https://gohelpdesk.org/docs/upgrading-1.3.0#reporter-attachments)
- Building from source needs Docker Buildx (#297, #301). [Details](https://gohelpdesk.org/docs/upgrading-1.3.0#buildx)
- `MFA_ENABLED` and `GUEST_SUBMISSION_ENABLED` are removed. [Details](https://gohelpdesk.org/docs/upgrading-1.3.0#env-removed)

**1.2.0**

- API keys and OAuth clients created through the admin UI stop working, because scopes are enforced. Everyone is signed out once. [Details](https://gohelpdesk.org/docs/upgrading-1.2.0#credentials)
- Notification email no longer carries ticket content. [Details](https://gohelpdesk.org/docs/upgrading-1.2.0#email)
- Webhooks to private addresses are refused, and existing internal hooks stop silently. [Details](https://gohelpdesk.org/docs/upgrading-1.2.0#webhooks)
- Also: the [GHD tracking prefix](https://gohelpdesk.org/docs/upgrading-1.2.0#tracking-prefix), a [Content-Security-Policy](https://gohelpdesk.org/docs/upgrading-1.2.0#csp), [email validation](https://gohelpdesk.org/docs/upgrading-1.2.0#email-addresses), [staff-only tags](https://gohelpdesk.org/docs/upgrading-1.2.0#tags) and a [TOTP lockout](https://gohelpdesk.org/docs/upgrading-1.2.0#totp).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, tests and pull request process, and [docs/DESIGN.md](docs/DESIGN.md), the specification.

## License

[GNU Affero General Public License v3.0](LICENSE). Modifications — including hosting as a service — must be released under the same license.
