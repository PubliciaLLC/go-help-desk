# Owner decisions, 2026-11-10 (binding for the pre-tag fixes and the search build)

Erik answered these with the question tool. Quotes are his words. Anything not quoted is
a recommendation he accepted in bulk ("Accept all") from `pretag-fixes.md` and `advanced-search.md`.

## Search (`advanced-search.md`)
- The UI as mocked up: approved ("Approve, start building").
- Reply-text search is in 1.3.0. "Search replies" is on by default in the page and off by default in the API.
- Requester filter: open to staff, limited to what they can see.
- Free-text requester: "free text on web, api keys need explicit freetext search permission". The scope is
  `requester_search:read`. It is never implied by another scope, and staff and admin keys and staff OAuth clients may hold it.
- The requester chip resolves the name by ID through `GET /users/lookup?id=`, which is session-only.
- One combined query serves every list request, so a search on "mine" is ranked by relevance.
- The reply index is built in the migration, with advice for large sites.
- Masking does not turn off name search.
- S5: measure query plans first; set `force_custom_plan` only if needed.
- S6: `sla=` while SLA tracking is off answers 400, and the filter is hidden in the UI.
- S7: any filter widens a staff search to everything they can see.
- S8: dates are in the viewer's timezone.
- S9: "I should be able to sort the search results, though not necessarily as a filter, but by clicking on headers
  in the table." The answer was "All visible columns": ticket number, subject, status, priority, SLA, created and
  updated. Clicking again reverses the order. The sort is kept in the URL. A text search starts sorted by relevance until a header is clicked.
  There are still no tags or custom fields as filters in 1.3.0.

## Pre-tag issues (`pretag-fixes.md`)
- All the plain D rows are accepted as recommended: D1-D3, D5, D7-D11, D13-D18, D20-D24, D26-D34, D36-D42.
  - **D18:** the channel filter is built as part of advanced search, not as a separate minimal filter.
- **D4 (attestation) and D6 (per-role passkey policy):** "Document both, 1.3.x". They are not built before the tag. DESIGN.md
  states the limits, and #616 parts (a) and (c) move to milestone 1.3.x after the tag.
- **D25:** "Enforce, mark breaking". Required custom fields are enforced on the API for every caller. This is listed under breaking changes and ships with #627.
- **D12:** "Build all three". The per-address login limiter moves out of memory too, so every #621 limiter is database-backed.
- **D35:** "Mask in every admin view". Apply the requester-name mask everywhere an admin sees requester names: the audit
  views, exports and API audit responses. Audit changes to the setting. Document that someone with database access can still read the names.
- **D40:** "Opt-in by name only". New `ticket.followed` and `ticket.unfollowed` webhook events. A hook receives them only if it lists them
  by name; "all events" hooks don't get them. The payload carries the person's ID, not their name. No email is sent.
- **D19:** the real-environment checklists still need a person; this is a separate decision for the owner.

## Earlier the same day
- A12: "Enforce it, update tests" (merged in #635).
- Reclassify: "Keep elapsed time", and "Cannot reclassify resolved and closed tickets" (#633).
- Audit names: "Keep name and host", and re-approve is audited (merged in #637).

## 2026-11-11
- **Reply search at scale.** Erik: "this needs to scale to hundreds of thousands, possibly millions of tickets", then chose "Stored column + caps":
  - a `tsvector` column on replies, filled online by a background job and a trigger;
  - its index built without blocking writes;
  - ranking capped to the N most recent reply matches;
  - a statement timeout on the reply arm, falling back to subject and description with `replies_searched: false`;
  - measured at 1M and 5M replies.
- **Reply-search time limit at 5M replies.** A word in 1% or more of 5M replies passed the 2 s limit on the measured machine and fell back to ticket text. Erik chose "Longer limit (e.g. 5 s)": the statement limit on the reply arm is 5 s, the 2,000-reply cap is unchanged.
- **Index-build warning:** "Bold warning + pre-build".
- **Requester-name mask:** "Keep the three values", and each now covers every admin view in its scope. An unresolved name shows as **"Deleted account"**.
- **Markdown formatter budget:** "Raise to 3 s".
- **`content-visibility: auto`** on long plain-text posts: "Ship it".
- **E2E against the production build** instead of the Vite dev server: "Use production build".
- **Audit Log change-detail search:** "Require a date range". It runs only with a date range of at most 90 days (the 90 is A's default, a named constant). Without a range, `q` still matches type, action and actor, and the page says to pick dates. The 100,000-entry window is dropped.
- **Custom-field value limits (#626/#627, G6).** Erik: "custom fields of type string default to 255 char limit. longtext caps at 10k chars". Built as: `text`, `number` (after trimming) and `select` 255 characters, `textarea` 10,000, counted in characters (code points), refused with `400` by every door before anything is created. Existing values over a limit stay as they are; an edit that does not send them is allowed. The index under the values is rebuilt on `(field_def_id, left(value, 255))` in migration 000065 so a 10,000-character value can be stored.
