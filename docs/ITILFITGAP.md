# Go Help Desk — ITIL / ITSM Fit-Gap Analysis

**Date:** 2026-09-09
**Baseline:** `docs/DESIGN.md` (v1–v4 roadmap) and the implementation on `main`
**Yardstick:** ITIL 4 practices (ITIL v3 process names noted where they differ)

> This is an assessment, not a scope proposal. Nothing here is committed work.
> Recommendations that would change the roadmap in `docs/DESIGN.md` are marked
> **[roadmap decision]** and need the owner's call before they become scope.

---

## 1. Method

Each ITIL 4 practice that a service desk tool can plausibly serve is scored:

| Score | Meaning |
|-------|---------|
| ✅ **Fit** | The practice is materially supported today |
| 🟡 **Partial** | Foundations exist; key elements are missing |
| ❌ **Gap** | Nothing in the system serves this practice |
| ⚪ **Out of tool scope** | An ITIL practice a help desk product is not expected to serve |

Findings cite code. Where the implementation diverges from `docs/DESIGN.md`,
that is called out separately — a spec/code divergence is a different kind of
problem from a roadmap gap, and it is fixed differently.

ITIL 4 defines 34 practices. 18 are assessed below; the remaining 16
(portfolio, project, strategy, workforce, architecture, financial management,
etc.) are organizational practices no ticketing tool implements.

---

## 2. Executive summary

**Go Help Desk is a strong service desk product and a weak ITSM product — and
that is exactly what `DESIGN.md` says it is.** Most gaps below are deliberate
roadmap decisions, not oversights. The analysis is useful for three things:

1. **Confirming the strong areas.** Service Desk and Information Security
   Management are genuine fits — better than many commercial tools at this
   stage. The CTI + custom-fields + group-scoping model is a well-chosen
   foundation that several ITIL practices can be built on without redesign.

2. **Three findings that are not roadmap gaps and want attention regardless of
   ITIL.** SLA tracking is specified in `DESIGN.md` as a working v1 feature but
   is materially non-functional: breach evaluation is never invoked, the
   pause-on-Pending rule is unimplemented, and the queue indicator does not
   exist in the frontend. Details in §3.6. These read as bugs against the
   existing spec, not as new scope.

3. **One structural observation.** Every significant ITSM gap — Incident vs.
   Request separation, Problem records, Change records, derived priority —
   traces to a single design fact: there is one `tickets` table and one
   lifecycle. That work is currently gated behind **v4 (SaaS)**, which couples
   an open-source data-model decision to a commercial packaging decision. Worth
   decoupling. **[roadmap decision]** — see §5.

**Headline scorecard: 2 Fit, 3 Partial, 11 Gap, 2 out of tool scope.**

---

## 3. Practice-by-practice assessment

### 3.1 Service Desk — ✅ Fit

*ITIL: single point of contact; multi-channel intake; triage, classification,
routing; user communication.*

**Have.** Web SPA intake for users and staff; guest submission with a tracking
number (`handler_tickets.go`); REST API with API-key and OAuth2 auth; an MCP
server (`internal/mcp/server.go`, 8 tools); three-level CTI classification;
group routing derived from CTI scope (`group_scopes`); assignment to a user or
a group; threaded replies with staff-only internal notes; attachments with
validation and optional ClamAV; tags; canned responses; live full-text search;
email and webhook notification out.

**Gap.**
- **No inbound email channel.** `internal/server/notify/email.go` is send-only.
  For most ITIL service desks email is the *primary* intake channel. This is
  the single highest-value addition in the whole analysis.
- **No channel attribution.** Tickets do not record how they arrived, so
  channel mix — a standard service desk metric — cannot be reported. A
  `source_channel` column is a one-migration change.
- No self-service portal distinct from the submission form (no catalogue
  browse, no knowledge search-before-submit deflection).

---

### 3.2 Incident Management — 🟡 Partial

*ITIL: restore normal service as quickly as possible; priority derived from
impact and urgency; major incident handling; functional and hierarchic
escalation.*

**Have.** A complete lifecycle (`New → In Progress → Pending → Resolved →
Closed`) with admin-customizable intermediate statuses; priority; user and
group assignment; a full status-change timeline (`ticket_status_history`,
surfaced on the ticket detail page); resolution notes; linked tickets; an audit
log behind every mutation.

**Gap.**
- **No ticket type discriminator.** Incidents are indistinguishable from
  requests, problems and changes (deferred to v4). Consequence: no
  incident-specific queues, workflows, or metrics — you cannot report incident
  volume, because nothing is an incident.
- **Priority is a manual free pick** (`domain/ticket/ticket.go:13`), not derived
  from Impact × Urgency. ITIL is explicit that priority is *derived*; a manual
  pick makes prioritization subjective and inconsistent across agents.
  Deferred to v4.
- **No major incident concept.** No major-incident flag, no stakeholder
  communication list, no bridge/war-room, no distinct major-incident workflow.
- **No functional escalation tiers.** Reassign-to-group is the only mechanism.
  There is no L1/L2/L3 tier model and no escalation record, so "how often does
  L1 resolve without escalating" is unanswerable.
- **No hierarchic escalation on SLA breach** — breaches are not detected at all
  (§3.6).
- No work log / time-spent capture, so effort per incident is not measurable.

---

### 3.3 Service Request Management — ❌ Gap

*ITIL: requests are pre-defined, pre-approved and standardized; fulfilled by a
repeatable workflow; frequently require approval; sourced from a catalogue.*

**Have.** Nothing that distinguishes a request from an incident. However, the
per-CTI custom fields subsystem (`internal/domain/customfield/`) is a genuinely
good foundation for request forms — it already supports per-node assignment
with `visible_on_new` and `required_on_new` flags, which is most of what a
request form needs.

**Gap.**
- No incident/request distinction (see §3.2).
- **No approval mechanism anywhere in the codebase.** No approver, no approval
  state, no approval record, no approval audit. Confirmed by search — there is
  no approval primitive to build on.
- **No fulfilment task model.** Multi-step requests (onboarding: create
  account → order laptop → grant access → schedule induction) cannot be
  represented; a request is a single ticket with a single assignee.
- No catalogue to request *from* (§3.8).

**Assessment.** This is the gap that most separates a *help desk* from an *ITSM
tool*. Approvals and fulfilment tasks are the two missing primitives.

---

### 3.4 Problem Management — ❌ Gap

*ITIL: problem records distinct from incidents; root cause; workarounds; a
known error database; proactive identification from incident trends.*

**Have.** The `parent_child` and `caused_by` link types
(`domain/ticket/ticket.go:69`) let a team group incidents under a ticket they
designate as "the problem" — by convention only. `DESIGN.md` itself gives this
example ("a Problem with multiple Incidents").

**Gap.** No problem entity; no root-cause or workaround fields; no known error
database; no proactive trend analysis (there is no reporting at all, §3.13); no
mechanism to surface a known workaround on incidents that match an open
problem — which is the practice's main day-to-day payoff.

**Note.** Problem Management is cheap once three other things exist: ticket
type (v4), a knowledge base (v3), and reporting (v3). It needs little unique
machinery beyond a workaround field and an article link.

---

### 3.5 Change Enablement — ❌ Gap

*ITIL: change records; standard / normal / emergency change types; risk and
impact assessment; authorization by a change authority or CAB; scheduled
implementation window; a change calendar with conflict detection; rollback
plan; post-implementation review.*

**Have.** Nothing. No approvals, no scheduling fields (no planned start/end),
no calendar, no risk assessment, no rollback plan, no PIR.

**Gap.** The entire practice.

**Roadmap realism flag.** `DESIGN.md` v4 lists "Change Request" as one of four
ITSM *ticket types*. A type label is not change enablement. Without approval
routing, a scheduled window, and a change calendar, a "Change Request" ticket
is an ordinary ticket with a different word on it. If change management is a
real goal, v4's scope as written understates the work by a wide margin.
**[roadmap decision]**

---

### 3.6 Service Level Management — 🟡 Partial, and weaker than `DESIGN.md` implies

*ITIL: targets agreed with the business, measured, reported and reviewed;
SLAs, OLAs and underpinning contracts; defined service hours.*

**Have.** `sla_policies` (priority + optional category, with response and
resolution targets in minutes); specificity-ordered policy matching; per-ticket
`sla_records`; first-response stamping on the first staff reply
(`domain/ticket/service.go:312`); admin CRUD and a policy management UI.

**Three divergences from `docs/DESIGN.md` — these are spec/code bugs, not
roadmap gaps:**

1. **Breach evaluation never runs.** `sla.Service.EvaluateBreaches`
   (`domain/sla/service.go:50`) is documented as "called on a schedule" but has
   no caller anywhere in the codebase. There is no scheduler: `cmd/server/main.go`
   starts exactly one goroutine, for graceful shutdown. `response_breached_at`
   and `resolution_breached_at` are therefore never populated in normal
   operation.
2. **SLA pause during Pending is unimplemented.** `DESIGN.md` states "SLA timers
   are paused while a ticket is in a Pending status." `IsResponseBreached` and
   `IsResolutionBreached` (`domain/sla/sla.go:33` and `:43`) compute deadlines
   directly from `ticketCreatedAt` with no pause accounting.
3. **The queue SLA indicator does not exist.** `DESIGN.md` specifies a
   green/amber/red per-ticket indicator in the ticket queue. The frontend has
   SLA policy administration only (`frontend/src/api/admin.ts:483`); no
   indicator is rendered anywhere.

Note that (1) also means the auto-close-after-reopen-window behavior described
in `DESIGN.md` never fires: `ListResolvedBefore` carries the comment "used by
the auto-close scheduler" (`domain/ticket/service.go:548`) and likewise has no
caller.

**Genuine ITIL gaps beyond those:**
- **No business hours / service calendar.** All targets are 24×7 wall-clock. A
  "4 hour response" target on a ticket raised at 17:00 Friday breaches before
  Monday. This is the largest correctness problem for real-world SLA use, and
  it is independent of the bugs above.
- No OLAs (internal team-to-team targets) or underpinning supplier contracts.
- No SLA attainment reporting and no breach notification or escalation — even
  once breaches are detected, nothing happens as a result.
- Targets anchor to ticket creation only. No per-status targets and no
  update-cadence targets ("customer updated at least every 24h").

---

### 3.7 Knowledge Management — ❌ Gap *(deliberate — v3)*

*ITIL: capture, curate and reuse knowledge; known errors; articles linked to
incident resolution; self-service deflection.*

**Have.** Canned responses (`internal/domain/cannedresponse/`) — reply
templates scoped globally, by category, or by category+type. Useful, but they
are outbound message boilerplate, not knowledge: no versioning, no reader
audience, no search, no article identity.

**Gap.** No articles; no public or self-service knowledge base; no
article↔ticket linkage; no "resolved using article X" attribution (which is
what makes knowledge reuse measurable); no deflection at submission time; no
article review/expiry lifecycle.

---

### 3.8 Service Catalogue Management — ❌ Gap

*ITIL: a customer-facing catalogue of requestable services with descriptions,
owners, targets and request forms.*

**Have.** The CTI tree, and per-CTI custom fields which function as per-service
request forms.

**Gap.** No service entity, no service owner, no customer-facing browse or
search, no per-service SLA, no catalogue-item→fulfilment workflow.

**Important distinction.** CTI answers *"what kind of thing is this ticket
about?"* — a classification for routing. A catalogue answers *"what can I ask
for?"* — an ordering surface. These are routinely conflated. Only the first
exists here, and the second cannot be derived from it: a category tree built
for routing rarely reads as a menu to an end user.

---

### 3.9 Service Configuration Management (CMDB) — ❌ Gap

*ITIL: configuration items, their relationships, ticket↔CI linkage, impact
analysis.*

**Have.** Nothing. Categories are a taxonomy, not CIs.

**Gap.** No CI entity; no CI relationships; no affected-CI on tickets; no
impact analysis; no discovery or import.

**Note.** This also gates *real* impact derivation: ITIL's Impact is meant to
reflect how many users or services a failing CI affects. Without a CMDB, an
Impact × Urgency matrix (v4) reduces to a second manual dropdown — better than
one manual dropdown, but not the practice.

---

### 3.10 IT Asset Management — ❌ Gap

No asset register, no ownership, no lifecycle/warranty/cost tracking. Commonly
expected alongside a help desk, since hardware tickets reference a device.
Distinct from CMDB (assets are financial/lifecycle; CIs are operational), and
frequently a separate product.

---

### 3.11 Monitoring & Event Management — ❌ Gap

*ITIL: events from monitoring tools become incidents automatically;
correlation; thresholds.*

**Have.** Webhooks are outbound only (`internal/server/notify/webhook.go`).
The practical workaround exists today: a monitoring tool with an API key can
`POST /api/v1/tickets`.

**Gap.** No inbound event endpoint with deduplication or correlation; no
alert→incident rules; no auto-close when an alert clears; no event-storm
suppression. Without dedupe, the API workaround produces one ticket per alert
repeat.

---

### 3.12 Release & Deployment Management — ⚪ Out of tool scope

Not served, and should not be. This lives in CI/CD tooling. Relevant only
insofar as change records reference releases. Recommend explicitly declaring it
out of scope in `DESIGN.md`.

---

### 3.13 Measurement & Reporting — ❌ Gap *(deliberate — v3)*

*ITIL: the practice that makes every other practice manageable.*

**Have.** The raw material is genuinely good and already being captured:
`audit_log` (before/after JSONB on every mutation), `ticket_status_history`
(every transition with actor and timestamp), `sla_records`, and timestamps on
every entity. **Nothing consumes any of it.** `DashboardPage.tsx` shows ticket
counts by status and a user count.

**Gap.** No MTTR or MTTA; no SLA attainment; no first-contact resolution; no
reopen rate; no backlog aging; no volume by category/agent/channel/time; no
CSV export; no scheduled reports.

**Assessment.** Highest return-on-effort item on the v3 list, because the data
model already supports it — this is a read-side and UI problem, not a schema
problem.

---

### 3.14 Continual Improvement — ❌ Gap

No improvement register, no structured post-incident review, no customer
satisfaction capture. CSAT — a rating prompt on resolution — is the cheapest
meaningful addition here and feeds §3.13 directly.

---

### 3.15 Information Security Management — ✅ Fit

**Have.** Role-based access (admin/staff/user) with CTI-derived scoping; TOTP
MFA with per-role enforcement; SAML 2.0 SSO with an admin local-auth failsafe;
bcrypt password hashing; HttpOnly session cookies; OAuth2 client-credentials
for integrations; hashed, scoped, expiring API keys; a full audit log; upload
validation with optional ClamAV scanning, image re-encoding, filename
obfuscation and SVG sanitization; HMAC-signed webhook payloads.

This is a stronger showing than most tools at comparable maturity.

**Minor gaps.**
- **The audit log is write-only from a user's perspective.** `audit_log` is
  populated and `auditstore.ListByEntity` exists, but no HTTP route exposes it —
  there is no admin audit view and no per-ticket activity feed. An audit trail
  nobody can read does not satisfy an auditor.
- No data retention or purge policy (relevant to GDPR/records management).
- No field-level encryption for sensitive ticket content; internal notes are the
  only confidentiality control.

---

### 3.16 Supplier Management — ❌ Gap

`Pending (waiting on user/vendor)` is a status *name* only. No supplier entity,
no third-party reference number on tickets, no supplier SLA or underpinning
contract tracking, no separate vendor-side clock. A ticket blocked on a vendor
is indistinguishable from one blocked on the customer — which also makes SLA
pause semantics ambiguous once §3.6 is fixed.

---

### 3.17 Relationship Management — 🟡 Partial

**Have.** User accounts, groups, reporter identity, guest contact details.

**Gap.** No customer/organization/department entity (v4 multi-tenancy implies
one); no VIP or priority-customer flag; no CSAT or feedback loop; no service
review cadence.

---

### 3.18 Availability & Capacity Management — ⚪ Out of tool scope

Correctly excluded. Would only become relevant with a CMDB and service
entities, for outage-driven major incidents and availability reporting.

---

## 4. Scorecard

| # | ITIL 4 practice | Score | Roadmap position |
|---|-----------------|-------|------------------|
| 3.1 | Service Desk | ✅ Fit | v1 — shipped |
| 3.15 | Information Security Management | ✅ Fit | v1 — shipped |
| 3.2 | Incident Management | 🟡 Partial | core v1; typing/priority matrix v4 |
| 3.6 | Service Level Management | 🟡 Partial | v1 — **partly non-functional** |
| 3.17 | Relationship Management | 🟡 Partial | implied by v4 |
| 3.3 | Service Request Management | ❌ Gap | v4 (type only) |
| 3.4 | Problem Management | ❌ Gap | v4 (type only) |
| 3.5 | Change Enablement | ❌ Gap | v4 (type only) |
| 3.7 | Knowledge Management | ❌ Gap | v3 |
| 3.8 | Service Catalogue Management | ❌ Gap | not on roadmap |
| 3.9 | Service Configuration Management | ❌ Gap | not on roadmap |
| 3.10 | IT Asset Management | ❌ Gap | not on roadmap |
| 3.11 | Monitoring & Event Management | ❌ Gap | not on roadmap |
| 3.13 | Measurement & Reporting | ❌ Gap | v3 |
| 3.14 | Continual Improvement | ❌ Gap | not on roadmap |
| 3.16 | Supplier Management | ❌ Gap | not on roadmap |
| 3.12 | Release & Deployment Management | ⚪ Out of scope | — |
| 3.18 | Availability & Capacity Management | ⚪ Out of scope | — |

---

## 5. Cross-cutting findings

**A. The single-entity ticket model is the root constraint.**
Incident/Request separation, Problem records, Change records and derived
priority are four symptoms of one cause: one `tickets` table, one lifecycle. A
`ticket_type` discriminator plus type-specific extension tables unlocks all
four at once. It is currently gated behind **v4 / SaaS**, which ties an
open-source data-model decision to a commercial packaging decision — the two do
not obviously belong together. **[roadmap decision]**

**B. There is no background job runner.** Auto-close, SLA breach evaluation,
breach escalation, scheduled reports, inbound email polling and change-window
reminders all require one. None work today, and two of them (auto-close, SLA
breach) are already described in `DESIGN.md` as working. One small scheduler
unblocks several practices at once and fixes two spec divergences.

**C. There is no approval primitive.** Service Requests and Change Enablement
both need it and neither can start without it. One generic `approvals` table
(subject type + id, approver user or group, state, decided_at, comment) serves
both practices.

**D. Intake is one-directional.** Outbound: email, webhooks. Inbound: web form
and REST API only. No inbound email, no inbound events. For a service desk this
is the most visible functional gap to an end user.

**E. Operational data is captured but never surfaced.** Audit log, status
history and SLA records are all written and none are readable. Three practices
(Reporting, Continual Improvement, Information Security) are each partly
blocked by what is essentially a missing read side.

---

## 6. Recommendations, sequenced

### Tier 1 — no roadmap change required

These close divergences from the *existing* spec, or are one-column additions.
Arguably maintenance, not new scope.

1. **Add a background scheduler** and wire auto-close and `EvaluateBreaches`
   into it. Fixes two behaviors `DESIGN.md` already promises. *(Cross-cutting
   finding B.)*
2. **Implement SLA pause during Pending** — specified in `DESIGN.md`,
   unimplemented in `domain/sla/sla.go`.
3. **Render the green/amber/red SLA indicator** in the ticket queue — specified
   in `DESIGN.md`, absent from the frontend.
4. **Expose the audit log** — an admin view plus a per-ticket activity feed. The
   data and the store method already exist; only a route and a page are missing.
5. **Add `source_channel` to tickets.** One migration; enables channel-mix
   reporting for free later.

### Tier 2 — aligned with the existing v3 scope, high return

6. **Reporting** (MTTR, SLA attainment, first-contact resolution, reopen rate,
   backlog aging, volume by CTI/agent/channel). Already-scoped for v3, and the
   data is already being captured — this is read-side work.
7. **Knowledge base** with ticket↔article linkage and a known-error flag. A
   known-error flag on articles covers a meaningful share of Problem Management
   for very little extra.
8. **CSAT on resolution.** Cheap; the only thing that makes Continual
   Improvement measurable.
9. **Inbound email-to-ticket.** The largest single gap in the Service Desk
   practice, which is otherwise the project's strongest area.

### Tier 3 — the structural ITSM work — **[roadmap decision]**

Currently v4. Recommend evaluating each on its own merits rather than as a
SaaS bundle.

10. **`ticket_type` discriminator + Impact × Urgency matrix.** The keystone —
    unblocks 11–13.
11. **Generic approvals primitive.** Unblocks Service Request Management
    properly, and is a prerequisite for 13.
12. **Problem records** — root cause, workaround, known-error linkage.
13. **Change records** — change type, risk, planned window, approval routing,
    change calendar with conflict detection, post-implementation review. Note
    this is substantially more work than v4 currently implies (§3.5).
14. **Minimal CMDB** — a CI entity and ticket↔CI linkage. Enables genuine
    Impact derivation rather than a second manual dropdown.

### Explicitly recommend *not* building

Full CMDB discovery and dependency mapping; release and deployment management;
capacity management; IT financial management. These are separate products with
mature incumbents. Integrate; do not rebuild.

---

## 7. Positioning note

Nothing above says the project is behind. `README.md` calls it a *help desk*,
`DESIGN.md` scopes it as one, and by that standard it delivers well. These are
only "gaps" against a goal of being marketed as an **ITSM** platform.

That positioning decision is worth making explicitly, because it determines
whether Tier 3 is essential or optional:

- **Stay a service desk.** Tier 1 and Tier 2 are sufficient. The product
  competes on being simple, self-hosted and honest — a real market, and the one
  `DESIGN.md` currently targets.
- **Become an ITSM tool.** Tier 3 is mandatory, and specifically the
  ticket-type / approvals / change-record chain. Change Enablement and Service
  Request Management are the two practices buyers actually check for when they
  are comparing against ServiceNow, Jira Service Management or Freshservice.

The middle path — ITSM ticket *types* without approvals, catalogue, or change
scheduling — gives the vocabulary of ITSM without the practices, and tends to
satisfy neither audience. That is the outcome most worth avoiding.
