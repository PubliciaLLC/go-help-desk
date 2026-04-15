# Go Help Desk — Design Questions

## Core Functionality

### 1. Ticket scope
HESK supports tickets with categories, priorities, statuses, custom fields, attachments, and canned responses. Which of these are must-haves for v1, and is there anything HESK does that you explicitly want to drop or change?

**Answer:**
These are the must-haves:
Categories (though I prefer the Category/Type/Item style that Remedy used), Priorities, Statuses (with ability to customize statuses), attachments for v1
We will add custom fields, and canned responses in v2

### 2. Knowledge base
HESK has a public-facing KB. Do you want one, and if so, should it be editable through the admin UI or managed externally (e.g. markdown files in a repo)?

**Answer:**
Knowledge base can be a v3 feature

### 3. Multi-tenancy
Single organization, or should this support multiple tenants (e.g. SaaS-style, each with their own categories/agents/settings)?

**Answer:**
v1 will be single-org (self-hosted). we will work on making v4 a SaaS product.

## Authentication & Authorization

### 4. SAML specifics
SAML for agents/staff only, or also for end-users submitting tickets? Should there be a fallback local auth (username/password), or is SAML the only login path?

**Answer:**
Local auth is default, SAML is available but not on by default. If SAML is enabled, only an admin should be able to login using username/password (as a failsafe).

### 5. IdP targets
Any specific IdPs you need to support (Okta, Azure AD/Entra, Google Workspace, etc.)? This affects metadata handling and quirk workarounds.

**Answer:**
All 3 of those. I'm open to suggestions on what else we should support.

### 6. Roles/permissions
HESK has admin vs. staff. Do you need a more granular RBAC model (e.g. category-scoped permissions, read-only auditors, team leads)?

**Answer:**
Admin, Staff (create tickets, assign tickets visible to them to any other staff or group, view tickets in their scope, edit tickets in their scope), open any ticket via search for ticket#, User (create/view/update their own tickets)

## Tech Stack

### 7. Language/framework
Do you have a preference? (e.g. Python/Django, Go, Node/Express, Rust, etc.) What about the frontend — server-rendered, SPA (React/Vue/Svelte), or API-only with no bundled UI?

**Answer:**
I'm open to suggestions. we can discuss pros/cons

### 8. Database
PostgreSQL, MySQL/MariaDB, SQLite for dev? Any existing infrastructure constraints?

**Answer:**
which is best if we want to go SaaS later? (not sqlite obvs)

### 9. Deployment target
Docker/Kubernetes, bare VM, serverless, or "whatever works"? This affects how the plugin system loads code.

**Answer:**
docker? if this is a bad idea, let me know

## Plugin Infrastructure

### 10. Plugin model
What should plugins be able to do? Some possibilities:
- React to ticket lifecycle events (created, assigned, resolved, etc.)
- Add custom fields or UI panels to tickets
- Add new API endpoints
- Integrate external systems (Slack notifications, JIRA sync, etc.)
- Modify/extend the UI

Which of these matter most?

**Answer:**
React to ticket events, custom fields/UI panels, external systems integration (Slack/Teams/Discord/JIRA,etc)
Themes?

### 11. Plugin trust level
Are plugins written by your team only (trusted, in-process), or should third parties be able to write them (sandboxed, restricted API surface)?

**Answer:**
both 1st and 3rd party plugins will be available

### 12. Plugin distribution
Loaded from local filesystem, installed from a registry, or configured via the admin UI?

**Answer:**
Registry or admin UI.  We can do admin UI in v1, and move to registry in v4

## REST API

### 13. API consumers
Is the API primarily for the frontend SPA, for external integrations (CI/CD, chatbots, monitoring tools), or both?

**Answer:**
all of the above. we should also allow an MCP interface.

### 14. API auth
API keys, OAuth2 client credentials, or piggyback on SAML sessions? Should there be scoped API tokens per integration?

**Answer:**
OAuth2 client credentials.  I think we may need another discusssion on this to better understand the options here.

## Operational

### 15. Notifications
Email is the obvious one. Do you also need webhook-based notifications, Slack/Teams integration, or is that plugin territory?

**Answer:**
Yes email + webhook

### 16. Search
Full-text search across tickets? If so, database-native (Postgres FTS) or something like Elasticsearch/Meilisearch?

**Answer:**
v3 feature. I'm okay with this being database-level feature

### 17. Scale expectations
Rough order of magnitude: tens of agents and hundreds of tickets/month, or thousands of agents and millions of tickets?

**Answer:**
dozens of agents, 1,000 tickets/month in v1, by the time we get to SaaS, we'll be at thousands of agents and millions of tickets, but most instances will not be that large.
