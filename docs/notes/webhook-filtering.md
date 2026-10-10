# Webhook filtering: notes for #413

Status: working notes, no code. This is a request for a feature that `docs/DESIGN.md` does not cover, so it needs a decision (and a version slot) before anyone builds it. Facts are from `origin/v1.3.0-beta`; line numbers will drift.

## The request

Let an administrator say *which tickets* a webhook fires for, so one Slack channel can get only critical tickets in one category and another channel gets everything else. Today a hook fires on two things only.

## How it works today

- A webhook is a row in `webhook_configs` (`authstore.WebhookConfig`): URL, a list of event names, an enabled flag, a payload format (`raw`, `slack`, `teams`, `discord`, `jira`), a write-only secret and the last delivery result.
- `hookSubscribes` (`backend/internal/server/notify/webhook.go`) decides whether a hook fires: the hook must be enabled, and its event list must contain the event type or `*` (an empty list, which the API no longer accepts, also means all). The admin UI sends `*` for "All events".
- The events are `ticket.created`, `ticket.assigned`, `ticket.status_changed`, `ticket.replied`, `ticket.resolved`, `ticket.closed`, `ticket.reopened` and `ticket.linked`. `guest.link_resent` never reaches a webhook.
- There is nothing finer than that: no condition on category, type, item, priority, group, assignee, requester or custom field.
- Webhooks are sent once and never retried; the result of the last attempt is shown on Admin → Webhooks.

## What a filter would need to see (the real constraint)

The webhook dispatcher receives only a `notification.Event` (`backend/internal/domain/notification/notification.go`), which has the event type, the ticket id, the actor, a free-form payload and the time. It does **not** carry the ticket. What the payload holds varies by event:

| Event | Payload keys today |
|---|---|
| `ticket.created` | tracking number, subject, **priority**, guest email (only for a guest ticket) |
| `ticket.replied` | tracking number, subject, an `internal` flag, the reply body (omitted for an internal note) and the reporter's email |
| `ticket.status_changed` | `new_status_id` |
| `ticket.linked` | `target_id`, `link_type` |
| `ticket.assigned`, `ticket.resolved`, `ticket.closed`, `ticket.reopened` | no payload at all (the event carries the ticket id, the actor and, outside the JSON, the subject and tracking number) |

So **category, type, item, group and assignee are in no event**, and priority is only in `ticket.created`. A filter on any of them cannot be evaluated from the event as it stands. There are two ways to give the dispatcher the data, and the choice is the real design decision:

1. **Enrich the event when it is raised.** The service that raises the event adds the classification fields it already has in hand (category id, type id, item id, priority, assignee group and user) to the payload or to typed fields. The filter then sees the ticket *as it was when the event happened*. Cost: the raw webhook body is a public contract ("raw subscribers receive today's full event"), and adding keys to the payload would change it; the repository already solved this once for the chat formats by adding fields that are excluded from JSON (see how `Subject` and `StatusName` are carried). The same technique would keep the raw body unchanged.
2. **Look the ticket up when delivering.** The dispatcher reads the ticket from the store. The filter then sees the ticket *as it is when the outbox worker runs*, which can be a different state from the one that triggered the event (a ticket reclassified between the event and the delivery). Cost: a read per event per hook (or per event, shared across hooks), and a new dependency: `WebhookDispatcher` holds only the webhook store and the base URL today, so it would need a ticket reader passed in.

Option 1 matches what a person means by "tell me when a critical ticket is created". Option 2 is simpler to add but can fire or not fire on data that changed in between.

## Questions to settle before building

1. **Which attributes?** Candidates: category, type, item, priority, group, status, assignee, requester role, custom fields. Each one costs an event field (option 1) or a lookup, plus a picker in the admin UI. A short first list (category, priority, group) probably covers the stated need.
2. **Shape of a rule.** One optional filter per hook (all conditions must match) is the smallest thing that answers the example. Several rules per hook (any of them matches) is a much bigger feature (rule ordering, a way to test them, a way to read them back). Which is wanted?
3. **Matching a category tree.** Categories, types and items are a hierarchy. Does "category Network" include its types and items? (Almost certainly yes; it needs saying.)
4. **Event time or delivery time?** Option 1 or 2 above.
5. **Archived or deleted values.** A filter that names a category later archived or deleted: does it keep matching the ticket data, or become invalid? The catalogue delete currently answers 500 when a category is in use (#418), which is a related gap.
6. **Visibility.** A hook with a filter should show on Admin → Webhooks , and the admin needs a way to check a filter without waiting for a real ticket (a "test" action that evaluates the filter against a chosen ticket and says why it would or would not fire).
7. **Authorisation and privacy.** Webhooks carry the full payload, subject and reply body included, to a target an administrator registered. A filter does not change what is sent, only when; it should not become a way to learn about tickets the administrator cannot see (they are an administrator, so they can see all, but the audit and masking settings should be reviewed for anything that assumes webhooks are unconditional).
8. **Version.** Not in v1 as DESIGN.md defines it; where does it go on the roadmap?

## Touch points if it is built (so the estimate is honest)

- `webhook_configs`: one new column (or a small related table) for the filter; a migration with a default that preserves "no filter".
- `authstore` store and its queries; the generated query code.
- `handler_admin_credentials.go`: create and update validation for the filter (known attribute names, ids that exist), with refusal codes in the same style as `invalid_event_name`.
- `notify/webhook.go`: the predicate, next to `hookSubscribes`, with the event carrying what it needs (option 1) or a ticket lookup (option 2).
- Event raising sites in `domain/ticket/service.go` (option 1).
- `frontend/src/pages/admin/WebhooksPage.tsx`: the filter controls and the list summary.
- `docs/DESIGN.md` (Notifications), `docs/api/openapi.yaml` (the webhook operations and schemas; the route test does not cover schemas, so this is a manual edit), the website's admin guide, and the docs this PR set refreshed.
- The webhook payload formats (`format.go`): unchanged, but the chat formats could show *why* a hook fired if that is wanted.

## Tests it would need

- Table-driven predicate tests: every attribute, matching and not matching, an empty filter, a filter on a value that no longer exists, a category-tree match.
- The existing webhook dispatch tests must pass unchanged with an unfiltered hook (no breaking change).
- A handler test for each refusal code.
- An integration test that raises a real event through the outbox and checks a filtered hook does and does not receive it (the outbox path is where an event is written and later decoded; a field excluded from JSON must survive that round trip, which is exactly the kind of thing that silently breaks).

## Risks and things to avoid

- Changing the raw body. Raw subscribers receive the full event today; their parsers must keep working.
- Evaluating the filter before the outbox write for some events and after it for others. Pick one place.
- A filter that fails open or closed on error: decide, and log. For a notification the safer default is to deliver (an extra message beats a missed one), but that should be a stated choice.
- Scope creep into a rules engine. The issue deliberately asks for a predicate, not conditions and actions.
