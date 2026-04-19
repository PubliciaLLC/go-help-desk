# Security Policy

## Supported Versions

Security fixes are issued for the latest `v1.x` minor release. Older
minor versions are not backported. Pre-1.0 development builds are not
supported.

| Version | Supported |
| ------- | --------- |
| 1.x     | ✅        |
| < 1.0   | ❌        |

## Reporting a Vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately via GitHub's private vulnerability reporting:

👉 https://github.com/PubliciaLLC/go-help-desk/security/advisories/new

Include as much of the following as you can:

- A description of the vulnerability and its impact
- Steps to reproduce, a proof-of-concept, or sample exploit code
- The affected version(s) and deployment mode (Docker Compose, bare binary, etc.)
- Any suggested mitigation or fix

## What to Expect

- **Acknowledgement** within 5 business days.
- **Initial assessment** (severity, reproducibility, affected versions) within 10 business days.
- **Fix and disclosure timeline** agreed with you based on severity. Default target: fix released within 90 days, coordinated public disclosure shortly after. Actively-exploited issues will be prioritized.
- **Credit** in the published GitHub Security Advisory unless you request otherwise.

We do not run a bug bounty program.

## Scope

**In scope:**

- The Go Help Desk backend (`backend/`)
- The React frontend (`frontend/`)
- Official Docker images and `docker-compose.yml` in this repository
- Default configuration and documented deployment paths

**Out of scope:**

- Vulnerabilities in third-party dependencies — please report those to the upstream project. If exploitability depends on how Go Help Desk uses the dependency, we still want to hear about it.
- User misconfiguration (e.g. running without TLS, weak administrator passwords, exposing the admin interface to the public internet without access control).
- Social engineering, physical attacks, and denial-of-service against gohelpdesk.org infrastructure.
- Findings from automated scanners without a demonstrated security impact.

## Safe Harbor

We consider security research conducted in good faith — including
reproducing vulnerabilities against your own test deployment — to be
authorized and welcome. We will not pursue legal action against
researchers who:

- Make a good-faith effort to avoid privacy violations, data destruction, and disruption of services other than their own test instance;
- Report findings promptly via the channel above;
- Give us a reasonable window to address the issue before public disclosure.

If you are unsure whether your intended research falls within this
scope, ask first via the private advisory channel.
