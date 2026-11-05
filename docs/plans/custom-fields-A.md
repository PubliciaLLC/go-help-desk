# Custom Fields admin, option A (1.3.0): design proposal

For Erik to approve before anything is built. Nothing here is implemented.
Checked against `origin/v1.3.0-beta` at `e7f94769`.

**Revision 4, final (2026-11-05).** This revision applies Erik's last
answers and adds the build plan (§10). His words appear in quotation marks
only where they were passed on exactly. Everything else is a proposal, or a
stated default where he gave no answer, and is marked that way. **No
questions remain open** (§12), so coding can start from §10.

Issue: #603. Erik chose "A in 1.3.0, B in 1.4".
- **A** fixes the page and the flow.
- **B** is out of scope: rules on each attachment for visibility by role,
  edit rights by role, and required-before-status.

The body of #603 still says "In 1.3.0 the page only gets wording fixes. The
redesign is planned for 1.4". That is out of date. It should be fixed later.
Nothing is posted now.

## Summary

- **Order.** Erik said: "Sort by field number, like task, for now. That
  changes in 1.5." He then clarified: "Just the field's number". He also
  chose "Keep the levels" for the new-ticket form and "Group it too" for the
  ticket page.
  - Both forms show the category's fields first, then the type's, then the
    item's.
  - Within each level, fields are sorted by the field definition's number,
    then by name.
  - The attachment's own `sort_order` is still stored and still returned, but
    nothing sorts by it.
  - Existing forms reorder on upgrade (§7).
- **All four existing bugs are fixed in 1.3.0** (Erik's decision). Each fix is
  its own small PR, and all four land before A (§2).
  - Bug 4: Erik chose "Show once".
- **Ticket creation through the API.** Erik said "Drop the value, but return a
  20X that's appropriate?" and then chose "201 + warnings list".
  - A value that is dropped is now reported in a new `warnings` array in the
    201 response (§3).
- **Deactivating a field** has these effects:
  - It hides the field from new tickets.
  - It hides the field from the admin list, unless **Show inactive** is on.
  - It makes the field read-only on tickets that hold a value.
  - The server refuses every write to an inactive field. Today a caller can
    still clear a value on one.
- **Options.** Options can be added, reordered, retired and brought back. An
  option can also be deleted, but only while no ticket holds it. Erik's words:
  "also allow delete, but the delete action, like the addition and retirement
  is audited", and "and the delete action is only valid if that option is
  unused".
- **Audit.** Erik chose "Every field change". Every admin change to a field
  definition, its options or its attachments writes an audit entry in the
  same transaction as the change (§5).
  - The audit view shows the field name, the option text and the place: Erik
    said "Show them".
  - Attachments removed when a category, type or item is deleted are audited
    too: "Yes, same transaction".
- **Number fields.** Erik chose "Validate now". `PUT` refuses a value that is
  not a number with `400`. Ticket creation drops it with the warning
  `not_a_number`. Bad values already stored stay as they are (§3).
- **Field type.** Erik chose "Locked everywhere". `PATCH` refuses a type change
  with `409` once any ticket holds a value for the field.
- **Migration.** Still only **000059**, which adds one nullable column:
  `custom_field_defs.retired_options`. Audit, delete, usage counts and
  warnings need no schema change.
- **New admin calls.** There are three: where a field is attached, the order
  of all fields, and how many tickets use a field and each of its options.
  - Erik said "Browser session only" for the first two.
  - The third is session-only too. That is A's stated default, following his
    rule for the other new calls. He did not rule on it separately.

---

## 0. What the code does today (beta)

### Two sort orders

| Number | Set by | Changeable in UI? | Governs today |
|---|---|---|---|
| `custom_field_defs.sort_order` | The number input on the create form only | No. There is no edit form. | The admin list (`ORDER BY sort_order, name`) and the **ticket page** card (`ListCustomFieldValuesForTicket ORDER BY d.sort_order, d.name`). The card is not grouped by level. |
| `custom_field_assignments.sort_order` | The Categories page sets it automatically to `assignments.length + 1` | No | The **new-ticket form**, within each level (`ListAssignmentsForScope ORDER BY a.sort_order`, with no tie-breaker). `ResolveFieldsForCTI` puts the category's fields first, then the type's, then the item's. |

### What ticket creation drops today (`handleCreateTicket`)

Custom fields are written after the ticket exists, as a best effort. Each case
below is dropped without a word, and the ticket is still created:

| Case | Where it is dropped |
|---|---|
| The key is not a UUID | `uuid.Parse` fails, so the entry is skipped |
| The field is not on this ticket's category, type or item | Not in the resolved set, so the entry is skipped |
| The field is inactive | `SetValue` returns `Invalid`, and the error is ignored |
| A select value is not one of the options | `SetValue` returns `Invalid`, and the error is ignored |
| Any other write error | The error is ignored |
| Resolving the ticket's fields fails | Every entry is skipped |
| An empty value | Skipped. This is not a drop: empty means "no value". |

**An invalid number is not dropped.** `SetValue` does not check `number`
fields. `"abc"` is stored as `"abc"`, on creation and on
`PUT /tickets/{id}/custom-fields` alike. This design changes that (§3,
"Number values").

### Inactive fields today

- `SetValue` refuses a **non-empty** value on an inactive field, with
  `400 bad_request`.
- An **empty** value goes to `DeleteValue` before the active check. So a
  caller can clear a value on an inactive field.
- `ResolveFieldsForCTI` does not filter on `active`. `NewTicketPage` does not
  check it either.

### Audit today

- **Nothing on custom fields is audited.**
- `audit.Entry` is generic: `EntityType`, `EntityID`, `Action`, `Before` and
  `After`.
- Two patterns exist for admin config:
  - **Email templates** (`emailtemplatestore.Update`) write the entry in the
    same transaction as the change, with `auditstore.New(q).Create`. A failure
    rolls both back.
  - **API keys** (`Server.writeAudit`) write it afterwards as a best effort,
    on a context that ignores cancellation, and log a failure.
- **The audit view shows only allow-listed keys** (`audit.shownKeys`). It drops
  any value that is a map or an array. It shows a placeholder for any key that
  looks like a secret.
  - The comment on `shownKeys` says: "Adding a key here is a decision that its
    values can never carry such data", meaning PII.
- **The Audit Log page:**
  - It shows `entity_type` as raw monospace text.
  - It shows the action as its name with underscores turned into spaces.
  - Its diff (`AuditDiff`) lists only keys that changed, and **renders nothing
    when Before or After is nil**. So a create or delete entry shows only its
    label.
  - The entity ID is not shown.
  - The entity-type filter offers `ticket`, `user`, `group`, `sla_policy` and
    `category`.

### Other facts the design depends on

- **Options storage.** `options JSONB` holds an array of strings. Commas are
  safe in an option. There is an index on
  `ticket_custom_field_values(field_def_id, value)`.
- **Store constructor.** `customfieldstore.New` takes only `*dbgen.Queries`.
  It has no database handle, so it cannot open a transaction today.
- **Auth on existing routes.** The existing routes use `RequireRole(admin)`,
  then `RequireMFA`, then `RequireResource(categories)`. None of them uses
  `DenyMachineCredentials`. The proposal keeps them as they are: Erik decided
  only about the new calls.
- **Requester responses.** A requester's ticket responses pass through
  `ticket.FilterForViewer`, which keeps only allow-listed keys.
  `requesterWrapperKeys` is `sla`, `can_reopen`, `replies` and `links`.
- **Guests** see no custom fields. **MCP** `create_ticket` takes no custom
  fields, and no MCP tool reads them.
- **Migrations.** Beta ends at 000058. No remote branch has 000059 or later
  (checked 2026-11-05).

---

## 1. DESIGN.md section (replaces "Field Definitions" and "Assignment to CTI Nodes")

> Drop-in text. It states rules and does not quote anyone.

```markdown
### Field Definitions

Defined globally under **Admin → Custom Fields**. Each definition has:

- **Name** (unique)
- **Type**: `text`, `textarea`, `number`, `select`. The type can be changed
  only while no ticket holds a value for the field; after that
  `PATCH /admin/custom-fields/{id}` refuses a change with `409 conflict`, and
  the admin page shows the type as fixed. A `number` field accepts only a
  number; see *Values*.
- **Options** (select only): an ordered list of the values that may be chosen,
  stored as a JSON array of strings, so an option may contain a comma. The admin
  page edits them as a list: add, edit, reorder, retire, bring back, delete.
  Blank and duplicate options are refused by the page.
- **Retired options** (select only): see *Options: retiring and deleting*.
- **Field number** (`sort_order`): orders fields within each level. Set by
  dragging on the Custom Fields page, with a pointer or the keyboard. See
  *Display order*.
- **Active flag**: field defs are never hard-deleted — only deactivated.

Every change an administrator makes to a definition, its options or its
attachments is written to the audit log in the same transaction as the change.
See *Audit*.

### Deactivating a field

A field can be deactivated whether or not tickets use it.

- It is not shown on the new-ticket form.
- On a ticket that holds a value it is still shown, read-only, to everyone who
  could see it before. On a ticket with no value it is not shown.
- Nobody can write it: `PUT /tickets/{id}/custom-fields` refuses a value for it,
  an empty one (a clear) included, with `400 bad_request`. On ticket creation a
  value for it is dropped and reported in `warnings` (see *Values*).
- The admin list hides inactive fields. **Show inactive** lists them, marked,
  each with **Reactivate**.

Reactivating restores the field as it was: deactivation does not touch its
attachments, options or number.

### Options: retiring and deleting

Retiring an option works the same way as deactivating a field. A retired option
is not offered on new tickets or in edits. Tickets that already hold it keep
showing it. The server refuses a write that sets a ticket to it, with
`400 bad_request`. **Show retired** in the options editor lists retired options,
each with **Bring back**.

An option can be deleted only while no ticket holds it. The page offers
**Delete** only on an option it has counted as unused. The server checks again,
under a lock on the definition row taken in the transaction that writes the
change, and answers `409 conflict` if any ticket holds the value. This applies
to any `PATCH` that removes a value from both lists, whichever client sends it.

Renaming an option that tickets hold retires the old text and adds the new text
in its place. Renaming an unused option edits it in place.

Retired options are stored in `custom_field_defs.retired_options`, a JSON array
of strings, separate from `options`. `options` holds only the options in use, so
every reader that predates retiring still gets a plain list of strings it may
offer. An option is in one list or the other, never both. A value enters
`retired_options` only from `options`. A select field needs at least one option
in use.

### Where a field appears

A field appears on a ticket only when it is attached to the ticket's category,
type or item. Creating a field attaches it nowhere, and the page says so: after
**Create field**, the new field opens on **Where it appears** with "Not on any
form yet".

Attachments are managed from either side, and both write the same
`custom_field_assignments` rows through the same per-node routes:

- from the field, on **Admin → Custom Fields**: **Where it appears** lists every
  category, type and item the field is attached to; **Add a place** picks a
  category, then optionally a type, then optionally an item;
- from the node, on **Admin → Categories**: the **Fields** subsection of an
  expanded category, type or item, as before.

Each attachment has:

- **Show on new-ticket form** (`visible_on_new`)
- **Required on new-ticket form** (`required_on_new`): only meaningful while
  shown; the page disables it when the field is not shown.

A field reaches a ticket once, however many of the ticket's category, type and
item it is attached to. The most specific attachment wins — item over type over
category — for its settings and for the level the field is shown at. The API
does not refuse an overlapping attachment. The Custom Fields page does not offer
one, and lists any that exist with a note saying which settings apply.

On the ticket page, staff see every active field the ticket has, empty or not,
and every inactive field that holds a value. Requesters see fields that hold a
value. Guests see no custom fields.

### Display order

Fields are grouped by level — the category's, then the type's, then the
item's — and within each level ordered by field number, lowest first, ties
broken by name. The new-ticket form and the ticket page use the same order, so
a field sits in the same place on both. A field's level is the level of its
most specific attachment. An inactive field that holds a value keeps its level.
A field that holds a value but is no longer attached to the ticket's category,
type or item comes after the item level, by field number then name, read-only.

`GET /tickets/{id}/custom-fields` keeps its own order (field number, then name);
the page applies the grouping.

`custom_field_assignments.sort_order` is still accepted, stored and returned by
the assignment routes, so no caller breaks, but nothing orders by it.

An assignment goes with the node it is attached to. Deleting a category, type or item deletes its assignments in the same statement (a category's include those of its types and items), so a delete that is refused for tickets in use leaves them alone. `custom_field_assignments.scope_id` has no foreign key, which is why this is done in the delete itself. Assignment routes check the whole path: the item must belong to the type, the type to the category, and the assignment to the scope the path ends at; a mismatch, or a scope that does not exist, answers `404`.

### Audit

Entries use entity type `custom_field` and the field definition's id, so a
field's whole history — attachments included — is one entity. Actions, and what
each records, are listed in the API documentation. Every entry carries the
field's name on both sides; option entries carry the option's text, and
attachment entries the place's path. The audit view shows all three: they are
text an administrator wrote, not ticket content. Attachments removed by
deleting a category, type or item are recorded as detached, in the same
statement as the delete.
```

The last paragraph under "Display order" is the existing text, unchanged.

In "Values", the sentence "Staff and admin can edit at any time after
creation, from the ticket detail page" gains "…, except a deactivated field,
which is read-only". Replace the guest/creation paragraph's description of
creation with this:

```markdown
On ticket creation, custom field values are written after the ticket exists
and never fail the request. A value that is not written is reported in the 201
response's `warnings` array, one entry per dropped field with a reason:
`invalid_field_id`, `not_on_ticket`, `inactive`, `retired_option`,
`not_an_option`, `not_a_number` or `not_saved`. An empty value means "no value"
and is not a warning. The key is absent when nothing was dropped.

A `number` field accepts a decimal number: after leading and trailing spaces
are removed, what Go's `strconv.ParseFloat` accepts, except NaN, infinity,
out-of-range values and hexadecimal. The trimmed text is stored as written, not
reformatted. `PUT /tickets/{id}/custom-fields` refuses anything else with
`400 bad_request`. Values stored before 1.3.0 are left as they are and shown as
they are; since the ticket page sends only the fields that changed, such a value
blocks only a save that changes that field.
```

---

## 2. Bug-fix PRs (1.3.0, before A, one PR each)

Erik decided that all four are fixed in 1.3.0. Bug 4's approach is also his
decision ("Show once"). Every other fix below is a proposal. Each PR starts
with a failing test.

### Bug 1: inactive fields still show and can still be written

- **New-ticket form.** `NewTicketPage` shows a field only when
  `visible_on_new && field_def.active`.
- **Resolver.** `ResolveFieldsForCTI` keeps returning inactive fields. Bug 2
  and the ordering both need them.
- **Server.** `SetValue` checks `active` before the empty-value branch. Any
  write to an inactive field, including a clear, returns the existing
  `Invalid("field %q is deactivated")` with `400 bad_request`. That is the
  error and code this path already uses.
- **Ticket creation.** The value is still dropped. It is reported through
  `warnings`, which arrives with A (§3).
- **Tests:**
  - `TestPutCustomFields_InactiveFieldRefusesAValue`
  - `TestPutCustomFields_InactiveFieldRefusesAClear` (fails today)
  - `TestPutCustomFields_InactiveFieldKeepsItsStoredValue`
  - Vitest: the form leaves out an inactive field, including a required one.

### Bug 2: fields not on the new-ticket form are unreachable

- **Data.** The ticket page's card resolves the ticket's fields with
  `/tickets/fields` for the ticket's category, type and item, and merges in
  `/tickets/{id}/custom-fields`.
  - The card keeps the order `/tickets/fields` returns. That order is already
    grouped by level. Sorting by number within each level is a server change,
    and it arrives with A-backend (§10), so bug 2 does not depend on it.
  - Detached fields that hold a value come after the item level, in the order
    `/tickets/{id}/custom-fields` returns them.
- **Staff and admin see:**
  - every active field the ticket has, empty or not, and editable;
  - every inactive field that holds a value, read-only and marked
    "Inactive";
  - every detached field that holds a value, read-only, after the item level.
  - An inactive field with no value is not shown.
- **Requesters** see the same fields they see today (those holding a value),
  in the grouped order.
- **Tests (vitest):**
  - an empty active field is shown to staff and can be filled;
  - an inactive field with a value is read-only;
  - an inactive field without a value is not shown;
  - a requester sees the same set of fields as today;
  - fields keep the resolved order, grouped by level;
  - a detached field comes last.

### Bug 3: one stale value blocks saving the whole card

- **Client.** Save sends only the fields whose value changed.
- **Server (proposal).** `PUT /tickets/{id}/custom-fields` writes everything
  in one transaction. The new store method `SetValues` does three things:
  1. It locks the named definitions `FOR SHARE`, in ID order.
  2. It checks every entry: the field is on the ticket, it is active, and a
     select value is one of the options in use.
  3. It writes every entry or none.
  The shared lock is also what makes option delete, type lock and
  deactivation free of races (§4, §5): an admin change holds `FOR UPDATE` on
  the same row. The call is wrapped in `dbretry` like the other transactions.
- **Store constructor.** This PR changes `customfieldstore.New(q)` to
  `customfieldstore.New(q, db)`, because the transaction needs the database
  handle. The call sites are `cmd/server/main.go`, two in `server_test.go`,
  and `migration_000039_orphaned_assignments_test.go`. A-backend builds on
  this.
- **Tests:**
  - `TestPutCustomFields_ARefusedEntryWritesNothing`
  - `TestPutCustomFields_AllEntriesWrittenTogether`
  - Vitest: Save sends only the changed field.
  - The concurrency tests against option delete and the type lock belong to
    A-backend, which adds the `FOR UPDATE` side.
  - Vitest: a ticket holding a retired option can still save another field.

### Bug 4: a field attached at two levels shows twice

- **Rule.** Erik chose "Show once". `ResolveFieldsForCTI` keeps one entry per
  `field_def_id`. The most specific attachment wins, item over type over
  category. Its `visible_on_new` and `required_on_new` apply, and the field is
  shown at its level.
- **API.** Nothing is refused.
- **Admin page.** The Custom Fields page does not offer overlapping places.
- **Tests:**
  - `TestResolveFields_FieldOnCategoryAndTypeAppearsOnce`
  - `TestResolveFields_MostSpecificAttachmentWins`. Table-driven over
    category+type, type+item and category+item. It checks the settings and
    the level.
  - `TestAttachOverlapping_IsNotRefused`

---

## 3. Ticket creation: `warnings` (Erik: "201 + warnings list")

`POST /api/v1/tickets` still answers **201** with the ticket as created. It
gains one key:

```json
{
  "id": "…", "tracking_number": "…", "subject": "…",
  "warnings": [
    { "field_id": "2f0c…", "reason": "inactive" },
    { "field_id": "91d3…", "reason": "retired_option" },
    { "field_id": "not-a-uuid", "reason": "invalid_field_id" }
  ]
}
```

- **`omitempty`.** The key is absent when nothing was dropped, so a response
  with nothing dropped is byte-for-byte what it is today.
- **`field_id`** is the key exactly as sent. That is why it is a string: for
  `invalid_field_id` it may not be a UUID.
- **Order.** The entries are sorted by `field_id`, so tests and clients see a
  stable order. Map iteration is random.

**Reasons** (proposal). Each one maps to a drop that exists today, except
`retired_option`, which A adds:

| `reason` | When |
|---|---|
| `invalid_field_id` | The key is not a UUID |
| `not_on_ticket` | The field is not on the ticket's category, type or item. This includes an ID that names no field. |
| `inactive` | The field is deactivated |
| `retired_option` | A select value is a retired option |
| `not_an_option` | A select value is in neither list |
| `not_a_number` | A `number` field's value is not a number (see "Number values" below). Erik: "Validate now". |
| `not_saved` | Any other failure, including the field lookup failing. Every non-empty entry gets this reason, and the error is logged. |

An empty value is not a warning: it means "no value".

**Who receives it.** The proposal is that staff, admins and requesters all
receive `warnings`. `"warnings"` is added to `requesterWrapperKeys` so that
`FilterForViewer` keeps it, and `requester_fields_enforced_test.go` is updated.
A requester only ever sees the IDs they sent themselves. Guests and MCP are
unaffected, because neither accepts custom fields.

**Implementation.** The create path calls `SetValues` in best-effort mode, in
one transaction. Valid entries are written, and the rest come back as
warnings. The response is a struct that embeds the ticket and adds
`Warnings []fieldWarning \`json:"warnings,omitempty"\``.

**OpenAPI.** `createTicket`'s 201 schema becomes
`allOf: [Ticket, {properties: {warnings: …}}]`, with a `CustomFieldWarning`
schema whose `reason` is an enum. Two descriptions change:
- the `custom_fields` description, "Unknown or empty entries are skipped",
  becomes "…skipped; skipped non-empty entries are listed in `warnings`";
- the operation description's sentence about custom fields gains the same
  clause.

**Tests:**
- `TestCreateTicket_WarningsNameEachDroppedField`. It is table-driven over
  every reason, and checks that the status is 201, the ticket exists, and the
  valid entries were written.
- `TestCreateTicket_NoWarningsKeyWhenNothingDropped`
- `TestCreateTicket_RequesterReceivesWarnings`
- `TestCreateTicket_EmptyValueIsNotAWarning`

### Number values (Erik: "Validate now")

**What counts as a number** (proposal, following the recommendation passed
on). The check is in `customfield` and is used by `SetValues` for `PUT` and
for creation alike:

1. Trim the value with `strings.TrimSpace`. If nothing is left, it is a clear,
   not a number: it is allowed, unless the field is inactive.
2. If the trimmed value starts with `0x`, `0X`, `-0x` or `+0x` (in any case),
   refuse it. `ParseFloat` accepts hexadecimal floats and underscores inside
   them. Nothing in the UI produces them, and the text would be hard to read
   in reports.
3. `strconv.ParseFloat(s, 64)` must return no error. An out-of-range value
   such as `1e400` returns `ErrRange` and is refused.
4. The result must not be NaN or ±Inf. This rules out `NaN`, `Inf` and
   `infinity`, which `ParseFloat` accepts.
5. The trimmed text is stored exactly as written. `1.50` stays `1.50`.

**What the frontend sends.** Both forms use `<input type="number">`.
- The browser gives either `""` (blank, or text it cannot parse) or a valid
  HTML floating-point number such as `12`, `-3.5`, `.5` or `1e3`.
- HTML does not allow a leading `+` or spaces in a valid floating-point
  number.
- Every value the browser can send passes the check above, so the UI never
  sees the new 400.

**Answers:**
- `PUT /tickets/{id}/custom-fields`: `400 bad_request`,
  `value "abc" is not a number for field "Cost"`. Nothing is written (bug 3's
  all-or-nothing rule).
- `POST /tickets`: the value is dropped with the warning `not_a_number`.

**Bad values already stored** stay as they are.
- They are shown as stored text in read mode.
- In edit mode the number input shows blank, because a browser cannot display
  non-numeric text in a number input. The field is sent only if the user
  types in it (bug 3), so leaving it alone does not erase or refuse anything.

**Tests:**
- `TestIsNumber`, table-driven:
  - accepted: `"12"`, `" 12 "`, `"-3.5"`, `".5"`, `"1e3"`, `"+1"`;
  - refused: `"abc"`, `"1,5"`, `"0x10"`, `"NaN"`, `"Inf"`, `"1e400"`,
    `"1_000"`;
  - `""` is a clear.
- `TestPutCustomFields_NumberFieldRefusesText` checks for 400 and that
  nothing is written.
- `TestPutCustomFields_NumberStoredAsWritten` checks that `" 1.50 "` is stored
  as `1.50`.
- `TestCreateTicket_NotANumberWarning`
- `TestPutCustomFields_StoredBadNumberDoesNotBlockOtherFields`
- A vitest test that an unchanged field with a bad stored number is not sent
  on Save.

---

## 4. Options: storage, usage counts, delete

### Storage: one new column (migration 000059)

| | Shape | Migration? | Older readers |
|---|---|---|---|
| **(1) A separate column. Recommended.** | New `retired_options JSONB NULL`. `options` holds only the options in use. | **Yes, 000059, schema only.** `ALTER TABLE custom_field_defs ADD COLUMN retired_options JSONB;`. A nullable column with no default changes only the catalogue, and no rows are rewritten. The down migration drops the column. | Unaffected. `options` keeps its key, its type and its meaning. |
| (2) Objects in the array | `options` changes shape | Yes, a data migration: every select field is rewritten | Broken, unless every serialiser turns the objects back into strings |
| (3) Overload the existing column | Two shapes in one column | None | Safe only if every decoder handles both shapes. Not recommended. |

**Compatibility under (1):**

- **`options` stays a plain list of strings everywhere.** That covers the
  admin list, `/tickets/fields`, and `/tickets/{id}/custom-fields`. The list
  holds only the options in use.
- **The admin calls** (`GET`, `POST`, `PATCH /admin/custom-fields`) also return
  `retired_options` (`omitempty`). `PATCH` accepts it as an optional field;
  leaving it out means no change.
- **The ticket routes** do not return `retired_options`.
- **MCP** has no custom-field tool, so nothing changes there.
- **The ticket page in edit mode** shows a held retired value as the current
  choice, labelled "(retired)" and disabled.

### Usage counts: `GET /api/v1/admin/custom-fields/{id}/usage` (new)

The page needs counts in two places: to offer **Delete** only on unused
options, and to show the type as fixed once the field is in use.

```json
{
  "tickets_with_value": 34,
  "values": [
    { "value": "Dell", "tickets": 12 },
    { "value": "HP",   "tickets": 0  }
  ]
}
```

- **`values`**:
  - For a select field, `values` has one entry for every option in use, every
    retired option, and every stored value that is in neither list. Options
    with no tickets show 0. Entries are in option order, then retired order,
    then the values in neither list.
  - For any other type, `values` is `[]`.
- **Queries:**
  - `SELECT count(*) FROM ticket_custom_field_values WHERE field_def_id = $1`
  - `SELECT value, count(*) FROM ticket_custom_field_values WHERE field_def_id
    = $1 GROUP BY value`, for select fields only.
  - Both read the existing `(field_def_id, value)` index.
- **Cost.**
  - Each query reads one index range: the rows for that one field. That is
    about one row per ticket that holds a value for it.
  - At tens of thousands of tickets this should take milliseconds. At a
    million it should take low hundreds of milliseconds. **These are
    estimates, not measurements.** The implementation PR should measure them
    on a seeded database.
  - The page calls it **once per field the admin expands**, not on the list.
    A count across all fields on every list load would read every value row
    in the table.
  - The count is advisory, because a value can be written after it is read.
    The server check in the next section is what enforces the rule.
- **Errors.** `400` for a bad ID, `404` for an unknown field. It is
  browser-session only, like the other new calls. That is A's stated default.

### Delete: race-free on the server (proposal)

`PATCH /admin/custom-fields/{id}` runs in one transaction:

1. `SELECT … FROM custom_field_defs WHERE id = $1 FOR UPDATE`
2. Compute the removed values: values that were in `options` or
   `retired_options` before and are in neither list now.
3. If any were removed, run
   `SELECT value FROM ticket_custom_field_values WHERE field_def_id = $1 AND
   value = ANY($2) LIMIT 1`. If it finds a row, answer **`409 conflict`** with
   "option %q is in use; retire it instead". This is the existing `conflict`
   code.
4. **Type lock**, with the same lock held: if `field_type` changes, run
   `SELECT EXISTS (… WHERE field_def_id = $1)`. If a row exists, answer
   **`409 conflict`** with "the type can't change once a ticket has a value
   for this field".
5. Write the definition and its audit entries (§5), then commit.

**Why there is no race.** Value writes lock the same definition row
`FOR SHARE` (bug 3). A value write that started first finishes before the
check runs, and the check sees its row. A value write that starts afterwards
waits for the commit and then sees the new lists, so it refuses the deleted
value.

**Rename.** Erik accepted the proposal that renaming retires the old text and
adds the new text. Editing an unused option in place is a proposal. The
server cannot tell a rename from a delete plus an add. It enforces only the
delete rule, and the audit records an `option_deleted` and an `option_added`.

---

## 5. Audit (Erik: "Every field change")

### Same transaction or best effort?

**Recommendation: the same transaction as the change.** This follows the
`ticket.Atomic` rule, and the email-template precedent in
`emailtemplatestore.Update`. There are three reasons:

1. **The audit log is the field's only history.** There is no versions table.
   Erik asked specifically that delete, addition and retirement be audited.
   That is the email-template case ("showing it is the history"). It is not
   the `writeAuditEntry` case in CLAUDE.md, where an entry "does not gate
   anything".
2. **The transaction is needed anyway.** The delete rule and the type lock
   (§4) already require a transaction with a row lock. Writing the entry
   inside it costs one extra insert.
3. **The caller cannot suppress the entry by hanging up.** If the request is
   cancelled, the change and its entry roll back together.

**The cost.** `customfieldstore.New` gains a `*sql.DB` parameter. That
happens in bug 3's PR, which needs it first (§2). Admin writes go through
store methods that lock, run a decide function supplied by the domain, and
write the change and its entries (the shape of `emailtemplatestore.Update`).
Entries are written with `auditstore.New(q).Create` inside the transaction, so
a failure rolls everything back. The domain imports `audit` only, which
`ticket` already does, so dependencies still point inward.

The alternative is best effort, after the change commits, on a context that
ignores cancellation (`Server.writeAudit`). It is simpler, but a crash or an
outage between the two writes loses the history entry.

### Entity type, actions, Before and After (proposal)

`entity_type` is `custom_field` and `entity_id` is the field definition's ID.
Every entry carries `field_name` on **both** sides, even when the name did not
change, so the audit page can say which field it is (see "Audit Log page"
below).

| Action | Written when | Before | After |
|---|---|---|---|
| `custom_field_created` | A field is created | nil | `field_name`, `field_type`, `sort_order`, `active` |
| `custom_field_renamed` | The name changes | `field_name` (old) | `field_name` (new) |
| `custom_field_type_changed` | The type changes (only while unused) | `field_name`, `field_type` | `field_name`, `field_type` |
| `custom_field_deactivated` | Active goes true → false | `field_name`, `active: true` | `field_name`, `active: false` |
| `custom_field_reactivated` | Active goes false → true | `field_name`, `active: false` | `field_name`, `active: true` |
| `custom_field_reordered` | A field's number changes (`PUT /order`, or `PATCH sort_order`). One entry per field whose number changed. | `field_name`, `sort_order` | `field_name`, `sort_order` |
| `custom_field_option_added` | A value appears in `options`, including on create, one entry per option | nil | `field_name`, `option_value`, `position` |
| `custom_field_option_moved` | An option's position in `options` changes, one entry per moved option | `field_name`, `option_value`, `position` | `field_name`, `option_value`, `position` |
| `custom_field_option_retired` | A value moves from `options` to `retired_options` | `field_name`, `option_value`, `retired: false` | `field_name`, `option_value`, `retired: true` |
| `custom_field_option_restored` | A value moves back from retired to in use | `field_name`, `option_value`, `retired: true` | `field_name`, `option_value`, `retired: false` |
| `custom_field_option_deleted` | A value leaves both lists (unused only) | `field_name`, `option_value` | nil |
| `custom_field_attached` | An attachment is created, from either page | nil | `field_name`, `assignment_id`, `scope_type`, `scope_id`, `scope_path`, `visible_on_new`, `required_on_new` |
| `custom_field_attachment_changed` | `visible_on_new`, `required_on_new` or `sort_order` changes | the same keys, plus `sort_order` | the same keys, plus `sort_order` |
| `custom_field_detached` | An attachment is deleted through its route, or removed because its category, type or item was deleted | the keys from `attached`, plus `node_deleted: true` when removed by a node delete | nil |

**Why the values are single values.** `Redact` drops any value that is an
array, so an options list would never display. That is why options get one
entry each, with single values. `scope_path` is a snapshot of the path at the
time, such as "Hardware › Laptop", so the entry still reads correctly after a
rename or delete.

**`position`** counts from 1.

**When a node is deleted (Erik: "Yes, same transaction").** Deleting a
category, type or item removes its attachments inside one delete statement in
`queries/categories.sql`. The proposal keeps it as one statement:

- The existing `gone` CTE adds `RETURNING` for the removed attachments.
- A further CTE does `INSERT INTO audit_log … SELECT … FROM gone JOIN
  custom_field_defs`, building `before` with `jsonb_build_object`. It records
  `field_name`, `assignment_id`, `scope_type`, `scope_id`, `scope_path`,
  `visible_on_new`, `required_on_new`, `sort_order` and
  `node_deleted: true`. The path names come from the same snapshot, so they
  are read before the node is gone.
- One statement means all of it happens or none of it does. If the delete is
  refused (`category_in_use` and the rest), the audit rows go with it.
- `categorystore` needs no database handle. `categorystore.New` is used in
  about 25 test files, and none of them changes.
- `DeleteCategory`, `DeleteType` and `DeleteItem` gain an `actorID
  *uuid.UUID` parameter. That covers the domain `category.Store`, the
  `category.Service`, its test fake, and `handler_admin_cti.go`. Nothing else
  calls them.

**Keys added to `audit.shownKeys`:**

- **Fixed values** (types, flags, numbers, IDs): `field_type`, `sort_order`,
  `active`, `position`, `retired`, `assignment_id`, `scope_type`, `scope_id`,
  `visible_on_new`, `required_on_new`, `node_deleted`.
- **Free text:** `field_name`, `option_value` and `scope_path`. Erik: "Show
  them". The comment block in `audit.go` gets a paragraph like the
  `template_*` one. It says these hold text an administrator wrote, never
  ticket content, and that no other writer may reuse the names.

The names are distinctive on purpose. Like the `template_*` keys, they are
shown for every entity type, so no other writer should reuse them.

### Audit Log page (proposal)

What the page does with these entries today:

- The Entity column shows `custom_field`.
- The action reads, for example, "Custom field option retired".
- `AuditDiff` shows the keys that changed: `retired: false → true`,
  `sort_order: 3 → 1`, `field_name: Dept → Department`.

That is not enough on its own. **Nothing says which field**, because
`field_name` does not change on most entries and `AuditDiff` hides keys that
did not change. Create and delete entries show only their label, because
`AuditDiff` renders nothing when one side is nil.

The proposal is to add three things:

- **A context line for `custom_field` entries.** A small
  `CustomFieldAuditContext` component shows `field_name`, plus `option_value`
  or `scope_path` when present, from whichever side exists. This follows the
  `RemovedTemplateText` precedent. For example:
  > Custom field option deleted
  > Department · option "Marketing"
- **`custom_field` in the entity-type filter.** The filter is also missing
  `email_template` and `api_key`. That is an unrelated gap, mentioned here but
  not fixed.
- **Tests** for the context line: a create entry, an option-deleted entry and
  an attached entry each show the field name and option or path. An entry
  with no allow-listed text shows only its label, without errors.

### Audit tests (Go)

- One test per action, table-driven. It makes the change through the HTTP
  route and checks `entity_type`, `entity_id`, `action`, the actor, and
  Before/After after `Redact`.
- `TestCustomFieldAudit_RolledBackWithTheChange`: an audit insert that fails
  (forced with a constraint violation) leaves the definition unchanged.
- `TestCustomFieldAudit_RefusedChangeWritesNoEntry`: a 409 delete and a 409
  type change write nothing.
- `TestCustomFieldAudit_ReorderWritesOnlyMovedFields`
- `TestCustomFieldAudit_AttachFromCategoriesRouteIsAudited`: the existing
  per-node routes write entries too.
- `TestDeleteCTINode_AuditsEachRemovedAttachment`: table-driven over
  category, type and item. A category delete writes one `custom_field_detached`
  per attachment on the category, its types and their items, with
  `node_deleted: true` and the path as it was.
- `TestDeleteCTINode_RefusedDeleteWritesNoAuditEntry`: a delete refused with
  `category_in_use` writes no entry. This extends
  `TestDeleteCategory_RefusedKeepsItsFieldAssignments`.
- `audit_allowlist_internal_test.go`: the new keys pass the check that no
  allow-listed name looks like a secret. `field_name`, `option_value` and
  `scope_path` contain no fragment on the deny list.

---

## 6. The new page

### Structure

There is one list of fields, sorted by field number. Each row expands in
place, as rows do on the Categories page.

- **Header.**
  - The title "Custom Fields".
  - The line *"Extra questions on a ticket. A field appears only where you
    attach it: to a category, a type or an item."*
  - The **New field** button and the **Show inactive** toggle.
  - There is no deactivate warning in the header.
- **Collapsed row.** Drag handle, name, type, and where the field appears:
  "Not on any form yet" (amber) or "On 3 places" (grey). An inactive row is
  listed only when **Show inactive** is on. It is greyed out, marked
  **Inactive**, and has **Reactivate**.
- **Expanded row.** Expanding a row loads `/assignments` and `/usage`. It has
  three sections:
  1. **Details.**
     - Name, which can be edited.
     - Type. It is a select while `tickets_with_value` is 0. Once the field is
       in use it is fixed text: *"Fixed: 34 tickets have a value for this
       field."*
     - **Options**, for a select field, with **Show retired**.
     - **Save** and **Cancel**.
  2. **Where it appears.**
     - One line per place, with "Archived" if the node is archived.
     - Each line has the **Show on new-ticket form** and **Required**
       checkboxes, and ×.
     - Below the lines, a sentence describing the effect, for example
       *"Shown when a ticket is filed under Hardware (any type). Required."*
       For an item place it adds *"Requesters don't pick items, so only staff
       will see this."*
     - **Add a place.**
  3. **Deactivate.**
- **Create form.**
  - The fields are Name, Type and Options (select only).
  - On **Create field**, the field gets max + 1 as its number, and its row
    expands.
  - Focus moves to **Add a place**, and a status banner says: *"Asset Tag
    created. It isn't on any ticket form yet. Choose where it appears."*
- **Add a place.**
  - A Category select, then Type ("Whole category" by default), then Item
    ("Whole type" by default, shown after a type is chosen).
  - Then the two checkboxes, with Show ticked by default, and **Add**.
  - A place that is already taken, or that would overlap, is disabled, and its
    text says why.
- **Options editor.**
  - Each option in use has a handle, a text input, a usage count, and
    **Retire**.
  - An option with a count of 0 also has **Delete**.
  - **Add option** is at the end. Blank and duplicate options are refused
    inline.
  - **Show retired** lists retired options with their counts and **Bring
    back**. A retired option with a count of 0 also has **Delete**.
  - If **Save** gets a 409 from the delete rule, the error is shown in place
    and the counts are reloaded.
- **Deactivate.** A `ConfirmDialog` with the text: *"Deactivate Asset Tag? It
  stops appearing on new tickets and can no longer be changed on any ticket.
  Tickets that already have a value keep showing it. It will be hidden from
  this list. Turn on Show inactive to reactivate it."*
- **Order hint**: *"Drag to set the order fields appear in. On a ticket,
  fields still appear by level: the category's first, then the type's, then
  the item's."*

### Saving

- **Reorder** is saved on drop with `PUT /admin/custom-fields/order`.
  - Inactive fields that are hidden keep their place relative to their
    neighbours. The full list is sent.
  - If the call fails, the old order comes back and the error is shown.
- **Places** are saved as soon as they change, through the existing per-node
  routes. The page then invalidates `['admin','custom-fields',id,…]` and the
  `['ctiFields']` prefix. The page no longer sends an attachment `sort_order`.
- **Name, options and retired options** are saved with **Save**, through the
  existing `PATCH`.

### Accessibility

- **Reorder** for both fields and options: dnd-kit `PointerSensor` +
  `KeyboardSensor` + `sortableKeyboardCoordinates`.
  - The handle is a `<button>` labelled, for example, "Reorder Asset Tag".
  - Space picks up, the arrow keys move, Space drops, and Escape cancels.
  - Each move is announced.
  - `touch-action: none` on the handle.
- **Touch targets** of at least 44 px on coarse pointers, using the shared
  `TOUCH_SIZE`/`ROW_BTN` classes moved from CategoriesPage. This covers every
  handle, ×, checkbox label, expand button, Retire, Delete, Bring back,
  Reactivate and Add.
- **Phone:** there is no table, and nothing scrolls sideways at 320 px.
- **Labels and announcements:** every input has a label. Errors use
  `role="alert"`. The create banner uses `role="status"`.

### Desktop wireframe (≥ 640 px)

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ Custom Fields                                                 [+ New field] │
│ Extra questions on a ticket. A field appears only where you attach it:      │
│ to a category, a type or an item.                       [ ] Show inactive   │
│                                                                              │
│ ✓ Asset Tag created. It isn't on any ticket form yet. Choose where it       │
│   appears.                                                                   │
│                                                                              │
│ Drag to set the order fields appear in. On a ticket, fields still appear    │
│ by level: the category's first, then the type's, then the item's.           │
│ ┌──────────────────────────────────────────────────────────────────────────┐ │
│ │ ⋮⋮  ▸ Department          Select     On 2 places                         │ │
│ │ ⋮⋮  ▸ Phone number        Text       On 1 place                          │ │
│ │ ⋮⋮  ▾ Asset Tag           Text       Not on any form yet                 │ │
│ │    ┌─ Details ─────────────────────────────────────────────────────────┐ │ │
│ │    │ Name [Asset Tag                 ]   Type [Text        ▾]          │ │ │
│ │    │                                                  [Save] [Cancel]  │ │ │
│ │    ├─ Where it appears ────────────────────────────────────────────────┤ │ │
│ │    │ Not on any form yet.                                              │ │ │
│ │    │ Add a place                                                       │ │ │
│ │    │ Category [Hardware      ▾]  Type [Laptop        ▾]                │ │ │
│ │    │ Item     [Whole type    ▾]                                        │ │ │
│ │    │ [x] Show on new-ticket form   [ ] Required          [Add]         │ │ │
│ │    ├───────────────────────────────────────────────────────────────────┤ │ │
│ │    │                                                   [Deactivate…]   │ │ │
│ │    └───────────────────────────────────────────────────────────────────┘ │ │
│ └──────────────────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────────────┘

With "Show inactive" on:
│ │ ⋮⋮  ▸ Old cost centre     Number     On 1 place   Inactive [Reactivate]  │ │

Expanded select field in use:
│    ┌─ Details ─────────────────────────────────────────────────────────┐   │
│    │ Name [Department          ]                                      │   │
│    │ Type  Select — fixed: 34 tickets have a value for this field     │   │
│    │ Options                                       [x] Show retired   │   │
│    │  ⋮⋮ [Finance                ] 20 tickets        [Retire]           │   │
│    │  ⋮⋮ [Sales, EMEA            ] 14 tickets        [Retire]           │   │
│    │  ⋮⋮ [IT                     ]  0 tickets        [Retire] [Delete]  │   │
│    │  [+ Add option]                                                   │   │
│    │  Retired                                                          │   │
│    │     Marketing   3 tickets                       [Bring back]      │   │
│    │     Legal       0 tickets                [Bring back] [Delete]    │   │
│    │                                                  [Save] [Cancel]  │   │
│    ├─ Where it appears ────────────────────────────────────────────────┤   │
│    │ Hardware                    [x] Show on new  [x] Required    ×    │   │
│    │ Network › VPN               [x] Show on new  [ ] Required    ×    │   │
│    │ Shown when a ticket is filed under Hardware (any type), required, │   │
│    │ or under Network › VPN.                                           │   │
│    │ [+ Add a place]                                                   │   │
```

### Phone wireframe (320–390 px)

```
┌────────────────────────────────┐
│ Custom Fields                  │
│ Extra questions on a ticket. A │
│ field appears only where you   │
│ attach it.                     │
│ [+ New field]                  │
│ [ ] Show inactive              │
│                                │
│ Drag to set the order within   │
│ each level.                    │
│ ┌────────────────────────────┐ │
│ │[⋮⋮] [▸] Department          │ │
│ │         Select · 2 places  │ │
│ ├────────────────────────────┤ │
│ │[⋮⋮] [▾] Asset Tag           │ │
│ │         Text · Not on any  │ │
│ │         form yet           │ │
│ │ Details                    │ │
│ │ Name                       │ │
│ │ [Asset Tag               ] │ │
│ │ Type                       │ │
│ │ [Text                  ▾] │ │
│ │ [Save]  [Cancel]           │ │
│ │ Where it appears           │ │
│ │ Hardware › Laptop      [×] │ │
│ │ [x] Show on new form       │ │
│ │ [ ] Required               │ │
│ │ Add a place                │ │
│ │ Category                   │ │
│ │ [Network               ▾] │ │
│ │ Type                       │ │
│ │ [Whole category        ▾] │ │
│ │ [x] Show on new form       │ │
│ │ [ ] Required               │ │
│ │ [Add]                      │ │
│ │            [Deactivate…]   │ │
│ └────────────────────────────┘ │
└────────────────────────────────┘
  [⋮⋮] [▸] [×] and checkbox rows: 44 px on touch
```

### Categories page changes (small)

- The Fields subsection gets the line *"Order is set on Custom Fields."* Each
  field name links to `/admin/custom-fields?field=<id>`.
- Its list is sorted by field number, because it uses the same query.
- Nothing else changes. Attaching and detaching from this page are audited on
  the server (§5).

---

## 7. Upgrade notes (1.3.0)

- **Field order changes on upgrade.** On both the new-ticket form and the
  ticket page, fields are grouped by level: the category's first, then the
  type's, then the item's. Within each level they are sorted by field number,
  then by name.
  - On the new-ticket form, the order within each level changes from
    "order attached" to "field number".
  - The ticket page used to show one list sorted by field number. It is now
    grouped by level.
  - **An install where every field has number 0 becomes alphabetical within
    each level.** To set an order, drag the fields on **Admin → Custom
    Fields**.
- **The attachment `sort_order` is ignored.** It is still accepted, stored and
  returned. No data is changed.
- **Deactivated fields no longer appear on the new-ticket form**, and nobody
  can change them, including clearing them. Values already stored still show.
  Deactivated fields are hidden from the admin list unless **Show inactive**
  is on.
- **A field attached at more than one level is shown once**, at its most
  specific level, with that level's settings.
- **Staff now see empty fields** on the ticket page.
- **`PUT /tickets/{id}/custom-fields` writes all entries or none.** The ticket
  page sends only fields that changed.
- **`POST /tickets` may include `warnings`.** The key is absent when nothing
  was dropped.
- **Number fields now accept only numbers.** `PUT /tickets/{id}/custom-fields`
  answers `400 bad_request` for a value that is not a number. It used to store
  any text. On ticket creation, such a value is dropped with the warning
  `not_a_number`. Values stored before the upgrade are left as they are and
  shown as they are.
- **Changes the API now refuses:**
  - `PATCH /admin/custom-fields/{id}` answers `409 conflict` when it changes
    `field_type` while any ticket holds a value for the field. It used to
    accept this.
  - It also answers `409 conflict` when it removes an option that any ticket
    holds, unless the option is moved to `retired_options`. It used to accept
    this too.
  - API clients that edit fields must retire in-use options rather than
    remove them.
- **Every admin change to a custom field is now audited**, in the same
  transaction as the change. Deleting a category, type or item also records
  each field attachment it removed. The Audit Log shows field names, option
  text and paths for these entries.
- **Migration 000059** adds the nullable column
  `custom_field_defs.retired_options`. No row is rewritten.
- **The new admin calls** (`/assignments`, `/order` and `/usage`) refuse API
  keys and OAuth clients. The existing custom-field routes still accept them.

---

## 8. API additions

The new routes sit inside the existing `r.Route("/custom-fields", …)`, after
`RequireRole(admin)`, `RequireMFA` and `RequireResource(categories)`, with
`.With(authmw.DenyMachineCredentials)`.

- For `/assignments` and `/order`, this is Erik's decision: "Browser session
  only".
- For `/usage`, it is A's stated default, following the same rule.

### `GET /api/v1/admin/custom-fields/{id}/assignments` (new)

Returns 200 with the places in tree order: category `sort_order, name`, then
type, then item, with the whole category first.

```json
[
  {
    "id": "8b1e…", "field_def_id": "2f0c…",
    "scope_type": "type", "scope_id": "77aa…", "sort_order": 3,
    "visible_on_new": true, "required_on_new": false,
    "category": { "id": "c1…", "name": "Hardware", "active": true },
    "type":     { "id": "77aa…", "name": "Laptop",  "active": true },
    "item":     null
  }
]
```

| Case | Answer |
|---|---|
| API key or OAuth client | refused by `DenyMachineCredentials` |
| `{id}` is not a UUID | `400 bad_request` |
| no such field | `404 not_found` |
| the field has no attachments | `200 []` |

Implementation:
- New sqlc query `ListAssignmentsForFieldDef`. It inner-joins categories, so
  an orphaned attachment cannot be returned.
- New domain type `customfield.Placement`.
- One new `Store` method. The test `fakeStore` implements it too.

### `PUT /api/v1/admin/custom-fields/order` (new)

```json
{ "field_def_ids": ["2f0c…", "91d3…", "0a7e…"] }
```

- The list must name every field definition exactly once, active and inactive
  alike.
- Position *n* (counting from 1) sets `sort_order = n`.
- Attachments are not changed.
- It returns 200 with the full list in the new order.

| Case | Answer |
|---|---|
| API key or OAuth client | refused by `DenyMachineCredentials` |
| invalid JSON, a missing key, or a value that is not a UUID | `400 bad_request` |
| the same ID twice | `400 bad_request` |
| the list does not match the stored fields | `409 conflict`: "the field list has changed; reload and try again". Nothing is written. |
| no fields exist and the list is `[]` | `200 []` |

How it works:
1. Lock every definition `FOR UPDATE`.
2. Check that the list matches the stored fields.
3. Run `UPDATE custom_field_defs d SET sort_order = o.ord FROM unnest(@ids::uuid[]) WITH ORDINALITY AS o(id, ord) WHERE d.id = o.id`.
4. Write one `custom_field_reordered` entry for each field whose number
   changed.
5. Commit.

### `GET /api/v1/admin/custom-fields/{id}/usage` (new)

The shape and cost are in §4. Errors: `400` for a bad ID, `404` for an
unknown field.

### Changes to existing calls (additive, except the two marked as refusals)

| Call | Change |
|---|---|
| `GET` / `POST` / `PATCH /admin/custom-fields[/{id}]` | `retired_options` is added (`omitempty`). `PATCH` accepts it as an optional field. **Refusals:** `409` for a type change while the field is in use, and `409` for removing an option a ticket holds. Every change is audited. |
| `POST /tickets` | `warnings` is added (`omitempty`), with the reasons in §3. It is still 201. |
| `GET /tickets/fields` | One entry per field. Fields are grouped by level, with each field at its most specific level, and sorted by number then name within each level. `field_def.sort_order` now carries the real number, where today it is always 0. |
| `GET /tickets/{id}/custom-fields` | No change to the shape or the order. |
| `PUT /tickets/{id}/custom-fields` | Refuses, with `400`, any write to an inactive field, a retired option, or a value for a `number` field that is not a number. A refusal writes nothing. |
| The per-node assignment routes | The shape is unchanged. Every change is now audited. |
| `DELETE` on a category, type or item | The response is unchanged. Each field attachment it removes is audited. |

---

## 9. Test plan

The tests for the bug-fix PRs are in §2, for warnings and number values in
§3, and for audit in §5. Go integration tests run against real Postgres
through `./scripts/test-db.sh`. Each worktree runs them with its own
`GHD_TEST_INSTANCE`, as CLAUDE.md requires for parallel runs.

### Go: domain (fake store)

- Reorder refuses a duplicate ID (`ErrValidation`), and refuses a list that
  does not match the stored fields (`ErrConflict`).
- An option in both lists is refused. So is a value that enters
  `retired_options` without having been in `options`, and a select field with
  no option in use.
- `SetValue`/`SetValues` refuses a retired option.
- The audit decide functions are table-driven: a given before/after pair of
  definitions produces exactly the expected list of entries.

### Go: store, handler, migration

- **Migration 000059.** `TestMigration059_AddsRetiredOptionsWithoutTouchingRows`.
- **Order.**
  - `TestFieldOrder_GroupedByLevelThenFieldNumberThenName`. The data has
    numbers that disagree across levels and attachment numbers that disagree
    with field numbers. Check `/tickets/fields`.
  - `TestFieldOrder_AllZeroIsAlphabeticalWithinEachLevel`.
  - `TestFieldOrder_MostSpecificAttachmentSetsTheLevel`.
  - `TestFieldOrder_ValuesRouteOrderUnchanged`: `/tickets/{id}/custom-fields`
    is still sorted by number then name.
- **Attachment `sort_order`.** It is still stored and returned.
- **Placements.** Paths and IDs; tree order; only that field's attachments;
  404, 400, and `[]` for none (add the route to `listshape_test.go`); archived
  nodes are marked.
- **Reorder.** Sets the numbers and leaves attachments alone. A list that
  doesn't match gets 409 and nothing changes. A duplicate gets 400.
- **Usage.**
  - Counts are right for options in use, retired options, and values in
    neither list.
  - A text field returns `values: []`.
  - The response is `[]`, not `null`.
- **Order, from the ticket page's view.** After A-backend, the bug 2 card
  shows fields sorted by number then name within each level. This is checked
  by the order tests above, because the card keeps the server's order.
- **Delete.**
  - Removing an unused option succeeds.
  - Removing an option a ticket holds gets 409 and nothing changes.
  - Moving the same option to `retired_options` succeeds.
  - `TestDeleteOption_ConcurrentValueWriteIsSerialised`: one goroutine sets a
    ticket to "X" while another deletes "X". Exactly one succeeds, and the
    database never ends up holding a deleted value.
- **Type lock.**
  - A change while the field is unused succeeds.
  - A change after one value exists gets 409.
  - A change after that value is cleared succeeds again.
  - `TestTypeLock_ConcurrentValueWriteIsSerialised`.
- **Deactivate.** `TestDeactivate_ConcurrentValueWriteIsSerialised`: a value
  write and a deactivation race. Afterwards, the database never holds a value
  written after the deactivation committed. This test is modelled on
  `group_delete_test.go`'s `AnAssignmentDuringTheDeleteWaitsThenFails`.
- **Retired options over REST.** `options` excludes retired values. The ticket
  routes have no `retired_options` key. A held retired value still comes back
  from `GET /tickets/{id}/custom-fields`. A `PUT` with a retired value gets
  400.
- **Auth** (table-driven, all three new routes):
  - staff are refused with 403;
  - a reporting user is refused with 403;
  - an admin without MFA is refused;
  - an admin's API key with `categories:write`, and an OAuth client, are
    refused, which is the `DenyMachineCredentials` result.
  - A test that the existing `GET /admin/custom-fields` still accepts that key.
- **Existing tests** pass unchanged: `handler_admin_responses_test.go`,
  `handler_admin_data_integrity_test.go` and
  `migration_000039_orphaned_assignments_test.go`. The only exception is the
  `customfieldstore.New` call sites, which gain the database handle.

### Vitest

**`CustomFieldsPage.test.tsx` (new)**

- **Header.** No "cannot be deleted" warning.
- **Deactivate.** The dialog text is right, it sends `PATCH {active:false}`,
  and the row disappears. **Show inactive** brings it back with
  **Reactivate**.
- **Create.** A select field with `Sales, EMEA` sends that text as one option.
  The new field gets max + 1 as its number. The banner appears and focus moves
  to Add a place.
- **Type.** The type can be changed while `tickets_with_value` is 0. When it
  is above 0, the type is fixed text with the count.
- **Options.**
  - Blank and duplicate options are refused inline.
  - **Retire** and **Bring back** move the value between the two lists.
  - **Delete** appears only when the count is 0, and Save sends the shorter
    list.
  - A 409 on Save shows the error in place and reloads the counts.
  - Renaming an option a ticket holds sends the old text in `retired_options`
    and the new text in `options`.
  - Renaming an unused option edits it in place.
- **Places.** The correct per-node `POST`/`PATCH`/`DELETE` paths. **Required**
  is disabled while **Show** is off. Overlapping places are disabled.
- **Reorder with inactive fields hidden.** Sends the full list, with hidden
  fields kept in their relative positions.

**`AdminAuditPage`**

- The `custom_field` context line for created, option-deleted and attached
  entries.
- `custom_field` appears in the filter.

**`CategoriesPage`**

- The field link, and the list sorted by field number.

### Playwright (`frontend/e2e/custom-fields.spec.ts`, using `mock.ts`)

- **Keyboard reorder of fields:** Space, ArrowDown, Space. This sends the
  `PUT /order` body, announces the move, and keeps focus on the handle.
- **Keyboard reorder of options**, then Save, sends the new `options` order.
- **Escape** cancels a reorder. **A pointer drag** sends the same `PUT`.
- **Phone** at 320, 375 and 390 px, with `hasTouch` and `isMobile`. Check
  these four states:
  - the list;
  - an expanded select field with **Show retired** on and the
    Delete/Retire/Bring back buttons shown;
  - Add a place;
  - **Show inactive** on.

  In each, check `scrollWidth <= width`, and that every control is at least
  44×44.
- **Sync.** Add a place on Custom Fields and check it on Categories. Remove it
  on Categories and check that it is gone on Custom Fields.
- **Create flow.** The banner appears, and Where it appears is open.
- Add `/admin/custom-fields` to `mobile-layout.spec.ts` if that spec lists
  admin pages.

---

## 10. Build plan

Eight PRs. Seven go against `v1.3.0-beta` and one goes against the website
branch. Each coder works in their own git worktree. Each PR is merged only
after the usual pass/fail review gate on its current head.

The **Owns** list in each PR is a contract. A coder who finds they need to
edit a file that is not on their list stops and asks the coordinator. The
only files more than one PR edits are the ones in the overlap table below.

**Rules for every PR:**
- Write the failing tests first (CLAUDE.md).
- Run Go integration tests with
  `GHD_TEST_INSTANCE=<pr-name> ./scripts/test-db.sh once`.
- Run frontend tests from `frontend/`: `npm test`, `npm run lint`,
  `npm run test:e2e`.
- `internal/dbgen` is generated code. Never hand-merge it. After any rebase
  that touches `backend/queries/`, run `sqlc generate` and commit what it
  produces.
- **Before writing any code, rebase onto the latest `v1.3.0-beta`, then check
  the newest migration number.** If 000059 is taken, use the next free number
  and update this document's references to 000059.

### PR-1 `cf-bug1-inactive`: inactive fields are hidden on new tickets and cannot be written

- **Scope:** bug 1 (§2).
  - The new-ticket form leaves out inactive fields.
  - `SetValue` checks `active` before the empty-value branch, so a clear is
    refused too.
- **Owns:**
  - `backend/internal/domain/customfield/service.go` (`SetValue` only)
  - `backend/internal/domain/customfield/service_test.go`
  - `backend/internal/server/handler_custom_fields_inactive_test.go` (new)
  - `frontend/src/pages/NewTicketPage.tsx`
  - `frontend/src/pages/new-ticket-inactive-field.test.tsx` (new)
- **Tests first:** the three Go tests and the vitest test listed in §2,
  bug 1.
- **Depends on:** nothing.

### PR-2 `cf-bug2-ticket-card`: staff see every field the ticket has

- **Scope:** bug 2 (§2).
  - Move `CustomFieldsPanel` out of `TicketDetailPage.tsx` into its own
    component, then change it there.
  - The move gives PR-3 and PR-6 a small file to edit, instead of a 900-line
    page.
- **Owns:**
  - `frontend/src/pages/TicketDetailPage.tsx`: only removes the inline panel
    and imports the new component.
  - `frontend/src/components/ticket/CustomFieldsPanel.tsx` (new)
  - `frontend/src/components/ticket/CustomFieldsPanel.test.tsx` (new)
- **Tests first:** the vitest tests in §2, bug 2.
- **Depends on:** nothing. Uses only existing endpoints.

### PR-3 `cf-bug3-atomic-put`: ticket field saves are all-or-nothing and send only changes

- **Scope:** bug 3 (§2).
  - `SetValues`: one transaction, definitions locked `FOR SHARE` in ID order,
    every entry checked, all written or none, wrapped in `dbretry`.
  - `PUT /tickets/{id}/custom-fields` uses `SetValues`.
  - `customfieldstore.New(q, db)`.
  - The card sends only the fields that changed.
  - Ticket creation is unchanged here. A-backend moves it onto `SetValues`.
- **Owns:**
  - `backend/queries/custom_fields.sql`: adds
    `LockCustomFieldDefsForShare`.
  - `backend/internal/dbgen/custom_fields.sql.go` and
    `backend/internal/dbgen/querier.go` (generated)
  - `backend/internal/domain/customfield/store.go`, `service.go` and
    `service_test.go` (the `fakeStore` gets `SetValues`)
  - `backend/internal/database/customfieldstore/customfieldstore.go`
  - `backend/cmd/server/main.go`: one line, the `customfieldstore.New` call.
  - `backend/internal/server/server_test.go`: two `customfieldstore.New`
    calls.
  - `backend/internal/database/migration_000039_orphaned_assignments_test.go`:
    one call.
  - `backend/internal/server/handler_custom_fields.go`: only
    `handlePutTicketCustomFields`.
  - `backend/internal/server/handler_custom_fields_atomic_test.go` (new)
  - `frontend/src/components/ticket/CustomFieldsPanel.tsx` and
    `CustomFieldsPanel.test.tsx`
- **Tests first:** §2, bug 3.
- **Depends on:**
  - **PR-1**, for `service.go`.
  - **PR-2**, which creates `CustomFieldsPanel.tsx`.
  - The backend half can be coded straight away. The frontend half is done
    once PR-2 has merged.

### PR-4 `cf-bug4-show-once`: a field attached at two levels shows once

- **Scope:** bug 4 (§2).
  - `ResolveFieldsForCTI` keeps one entry per field. The most specific
    attachment wins, for its settings and for its level.
  - The ordering change within levels is **not** in this PR. It is in PR-5.
- **Owns:**
  - `backend/internal/domain/customfield/service.go` (only
    `ResolveFieldsForCTI`)
  - `backend/internal/domain/customfield/service_test.go`
  - `backend/internal/server/handler_custom_fields_resolve_test.go` (new)
- **Tests first:** §2, bug 4.
- **Depends on:** **PR-1**. Both edit `service.go` and `service_test.go`, in
  different functions, so the rebase is mechanical.

### PR-5 `cf-a-backend`: everything on the server for A

- **Scope:**
  - Migration 000059 and `retired_options` (§4).
  - The three new routes (`/assignments`, `/order`, `/usage`), all with
    `DenyMachineCredentials` (§8).
  - The order change: `ListAssignmentsForScope ORDER BY d.sort_order,
    d.name`, and it selects `d.sort_order`.
  - Option delete and the type lock under a `FOR UPDATE` row lock (§4).
  - Number validation and `not_a_number` (§3).
  - Ticket creation moves onto `SetValues`, with `warnings` (§3).
  - Audit for every change, in the same transaction, plus the node-delete
    cascade and the `shownKeys` additions (§5).
  - The OpenAPI spec for everything above, plus bug 1's and bug 3's changes
    to `PUT /tickets/{id}/custom-fields`, which those PRs deliberately leave
    undocumented in the spec.
- **Owns:**
  - `backend/internal/database/migrations/000059_custom_field_retired_options.up.sql`
    and `.down.sql` (new)
  - `backend/queries/custom_fields.sql` and `backend/queries/categories.sql`
  - `backend/internal/dbgen/custom_fields.sql.go`, `categories.sql.go`,
    `models.go` and `querier.go` (generated)
  - `backend/internal/database/customfieldstore/customfieldstore.go`
  - `backend/internal/database/categorystore/categorystore.go`
  - `backend/internal/domain/customfield/customfield.go`, `service.go`,
    `store.go` and `service_test.go`
  - `backend/internal/domain/customfield/audit.go` (new, the decide
    functions) and `audit_test.go` (new)
  - `backend/internal/domain/customfield/number.go` (new) and
    `number_test.go` (new)
  - `backend/internal/domain/category/service.go`, `store.go` and
    `service_test.go` (the actor parameter)
  - `backend/internal/domain/audit/audit.go` and
    `audit_allowlist_internal_test.go`
  - `backend/internal/domain/ticket/requester_view.go` (`warnings` in
    `requesterWrapperKeys`)
  - `backend/internal/server/handler_custom_fields.go`, `routes.go`,
    `handler_tickets.go` (create only) and `handler_admin_cti.go` (passes the
    actor to the three deletes)
  - Existing tests to extend: `backend/internal/server/requester_fields_enforced_test.go`,
    `listshape_test.go` and `handler_admin_data_integrity_test.go`
  - New tests:
    - `backend/internal/server/handler_custom_field_admin_test.go`
    - `handler_custom_field_audit_test.go`
    - `handler_create_ticket_warnings_test.go`
    - `handler_custom_field_number_test.go`
    - `backend/internal/database/migration_000059_retired_options_test.go`
    - `backend/internal/database/custom_field_locks_test.go`
  - `docs/api/openapi.yaml`
- **Tests first:** §3 (warnings, numbers), §5 (audit), and §9's "Go: domain"
  and "Go: store, handler, migration" lists.
- **Depends on:** **PR-1, PR-3 and PR-4**, because all three touch
  `service.go`, `customfieldstore.go`, `handler_custom_fields.go`,
  `custom_fields.sql` and the generated code.
  - Start coding from PR-3's branch once PR-3 is open. Rebase onto beta once
    PR-1, PR-4 and PR-3 have merged.
  - PR-2 is not needed.
- **Size:** this is the largest PR, about 5–6 days.
  - If it needs splitting, the clean cut is the node-delete cascade. Move
    `queries/categories.sql`, `categorystore.go`, `domain/category/*` and
    `handler_admin_cti.go` into a PR-5b that merges after PR-5. PR-5b shares
    only `audit.go` (`node_deleted`) and the generated code with PR-5.

### PR-6 `cf-a-frontend`: the Custom Fields page and everything it touches

- **Scope:** §6.
  - The page is rewritten, with the options editor (counts, retire, bring
    back, delete), the place picker, Show inactive, and the type lock.
  - A shared sortable list helper with keyboard support, and shared touch
    classes.
  - The Categories page gets its link and text.
  - The ticket card labels a held retired option "(retired)".
  - The Audit Log context line, and `custom_field` in its filter.
  - `?field=<id>` deep links.
- **Owns:**
  - `frontend/src/pages/admin/CustomFieldsPage.tsx` (rewrite)
  - `frontend/src/pages/admin/CategoriesPage.tsx`: the link, the text, and
    importing the moved touch classes.
  - `frontend/src/components/admin/SortableList.tsx` (new)
  - `frontend/src/components/admin/touchTargets.ts` (new: `TOUCH_SIZE`,
    `ROW_BTN`)
  - `frontend/src/components/admin/CustomFieldAuditContext.tsx` (new)
  - `frontend/src/pages/admin/AdminAuditPage.tsx`
  - `frontend/src/api/admin.ts` and `frontend/src/api/types.ts`
  - `frontend/src/router.tsx`: `validateSearch` for `?field=` on the
    custom-fields route.
  - `frontend/src/components/ticket/CustomFieldsPanel.tsx` and its test (the
    retired label)
  - New tests:
    - `frontend/src/pages/admin/CustomFieldsPage.test.tsx`
    - `frontend/src/pages/admin/categories-field-link.test.tsx`
    - `frontend/src/components/admin/CustomFieldAuditContext.test.tsx`
    - `frontend/e2e/custom-fields.spec.ts`
  - Existing tests to extend: `frontend/src/pages/admin/AdminAuditPage.test.tsx`,
    `frontend/e2e/mock.ts` and `frontend/e2e/mobile-layout.spec.ts`.
- **Tests first:** §9's Vitest and Playwright lists.
- **Depends on:**
  - **PR-5**, for the API it calls. It can be coded at the same time,
    against the shapes in §8 using mocks. It merges after PR-5.
  - **PR-3**, for `CustomFieldsPanel.tsx`.

### PR-7 `cf-a-docs`: DESIGN.md and the upgrade notes

- **Scope:**
  - In `docs/DESIGN.md`, replace "Custom Fields → Field Definitions /
    Assignment to CTI Nodes" with the text in §1, and change "Values" as §1
    says.
  - In `docs/upgrade-notes-1.3.0.md`, add the items in §7:
    - the two new refusals and the number refusal go under **Breaking
      changes**;
    - `warnings`, the new admin calls and audit go under **New features** and
      **Changed behaviour**.
  - `README.md`'s feature line ("Custom fields, tags, …") stays as it is.
- **Owns:** `docs/DESIGN.md` and `docs/upgrade-notes-1.3.0.md`.
- **Tests:** none. Check every rule in it against the merged code, not
  against this design.
- **Depends on:** it can be written at the same time as the others. It
  merges **after PR-5 and PR-6**, so it describes what shipped.

### PR-8 `cf-a-site`: the website (against `docs/web-1-3-0`, not beta)

- **Scope:** update the website pages that describe custom fields, and copy
  the spec.
  - The site is a separate branch with no history in common with beta.
    `docs/web-1-3-0` is the 1.3.0 site, and `gh-pages` is the live one.
  - Proposal: this PR targets `docs/web-1-3-0`. Publishing to `gh-pages`
    happens with the rest of the 1.3.0 site.
- **Owns** (on that branch):
  - `docs/admin-guide.html`: the custom fields section.
  - `docs/tickets.html`: how fields show on tickets.
  - `docs/upgrading-1.3.0.html`: the same items as §7.
  - `docs/openapi/openapi.yaml`: copied from beta's `docs/api/openapi.yaml`
    after PR-5 merges.
- **Depends on:** **PR-5**, for the spec copy, and **PR-7**, for the wording.
  It touches no file in beta.

### Files edited by more than one PR

| File | PRs, in merge order | Why it is safe in this order |
|---|---|---|
| `backend/internal/domain/customfield/service.go` | PR-1 → PR-4 → PR-3 → PR-5 | Each PR edits different functions: `SetValue`, then `ResolveFieldsForCTI`, then `SetValues`. PR-5 builds on all three. |
| `backend/internal/domain/customfield/service_test.go` | PR-1 → PR-4 → PR-3 → PR-5 | The tests are added, not changed. PR-3's `fakeStore` change comes before PR-5's. |
| `backend/internal/domain/customfield/store.go` | PR-3 → PR-5 | PR-3 adds `SetValues`. PR-5 adds the rest. |
| `backend/internal/database/customfieldstore/customfieldstore.go` | PR-3 → PR-5 | PR-3 adds the constructor and `SetValues`. PR-5 builds on them. |
| `backend/internal/server/handler_custom_fields.go` | PR-3 → PR-5 | PR-3 changes only the PUT handler. |
| `backend/queries/custom_fields.sql` and `backend/internal/dbgen/*` | PR-3 → PR-5 | PR-5 regenerates the code after rebasing. |
| `frontend/src/components/ticket/CustomFieldsPanel.tsx` and its test | PR-2 → PR-3 → PR-6 | PR-2 creates it. PR-3 changes Save. PR-6 adds the retired label. |

No other file is edited by two PRs:
- `main.go` and `server_test.go` are edited only by PR-3.
- `NewTicketPage.tsx` only by PR-1.
- `TicketDetailPage.tsx` only by PR-2.
- `openapi.yaml` only by PR-5.
- `DESIGN.md` and the upgrade notes only by PR-7.

### Order and parallel work

**Merge order:** PR-1 → PR-4 → PR-2 → PR-3 → PR-5 → PR-6 → PR-7, then PR-8
on the website branch. PR-2 has no dependencies, so it can merge at any point
before PR-3.

**What can be coded at the same time:**

| Start now, in parallel | Starts once | Notes |
|---|---|---|
| PR-1, PR-2, PR-4, PR-7 (draft) | — | They do not share code. PR-4 rebases onto PR-1 before merging, which is a mechanical rebase. |
| PR-3 backend half | — | The frontend half waits for PR-2 to merge. |
| PR-5 | PR-3 is open | Branch from PR-3's branch, then rebase onto beta after PR-3 merges. |
| PR-6 | now, using mocks | Code it against §8's shapes. Rebase after PR-3 and PR-5 merge, then run against the real API. |
| PR-8 | PR-5 and PR-7 have merged | Different branch. |

So at the start, six coders can work at once: PR-1, PR-2, PR-3, PR-4, PR-6
and PR-7. PR-5 follows as soon as PR-3 is open.

---

## 11. Effort, risk, and what to leave out

### Effort

| Part | Size |
|---|---|
| Bug 1 (form filter, server refuses writes to inactive fields) | ~2 h |
| Bug 2 (staff card, grouped order) | ~½ day |
| Bug 3 (send only changes, one transaction for `SetValues` with `FOR SHARE`, concurrency test) | ~½ day |
| Bug 4 (one entry per field, most specific wins) | ~2 h |
| Ticket creation `warnings` (handler, filter key, OpenAPI, tests) | ~½ day |
| Number validation (check, `PUT` refusal, `not_a_number`, tests) | ~½ day |
| Auditing attachments removed by a node delete (three statements, actor parameter, tests) | ~½ day |
| A backend: migration 000059, retired options, placements, order, usage, delete and type lock under a row lock, the order change, the `customfieldstore` constructor | ~2½ days |
| Audit: decide functions, entries for 14 actions, writes in the same transaction, `shownKeys`, Audit Log context line, tests | ~2 days |
| A frontend: page rewrite, options editor with counts, retire and delete, place picker, Show inactive, type lock, shared sortable helper | ~3 days |
| Vitest, Playwright, DESIGN.md, OpenAPI for the admin calls | ~1½ days |
| **Total** | **~12–13 days of work**, across the eight PRs in §10. Done in parallel as §10 describes, the critical path is PR-1 → PR-4 → PR-3 → PR-5 → PR-7, about 8–9 days. |

### Risks

- **Every existing form reorders on upgrade.** This follows from Erik's
  ordering decision. An install where every field is 0 becomes alphabetical
  within each level.
- **Three API calls are now refused** (§7):
  - a type change while the field is in use (409);
  - removing an option a ticket holds (409);
  - a value for a number field that is not a number (400).

  All three follow from Erik's decisions. An API client that relied on any
  of them will start getting errors.
- **The row locks are new.** Value writes take `FOR SHARE` on definitions, and
  admin writes take `FOR UPDATE`. The order is fixed (by ID), and every
  transaction is wrapped in `dbretry`. The concurrency tests in §9 are what
  prove it works. **I have not run any of this.**
- **The usage count** is an estimate until it is measured. It is fetched only
  when a field is expanded.
- **Audit volume.** Creating a select field with 20 options writes 21 entries.
  Reordering writes one entry per field that moved. Retention handles growth.
  The proposal accepts this, because an options list as an array would not
  display at all.
- **Migration 000059.** If another branch takes the number first, the check
  from #307 fails loudly and the number goes up by one.
- **dnd-kit keyboard mode** works but is hard to discover. The fallback is Move
  up / Move down buttons.
- **The bug 2 fix changes what staff see.** Erik decided the bug is fixed. The
  fix itself is a proposal. This note is here because the change is visible.

### Leave out of 1.3.0

- All of B, and conditional fields.
- Order per node. Erik: "That changes in 1.5".
- A live preview of the new-ticket form.
- Server-side refusal of blank or duplicate options. It would refuse input the
  API accepts today.
- Keyboard reorder on the Categories page.
- Checking "required" on the server.
- Cleaning up number values stored before the upgrade.

---

## 12. Open questions

**None.** Every question from earlier revisions has an answer from Erik or a
stated default, listed below. Coding can start from §10.

### Answered by Erik (2026-11-05)

1. **Ordering:** "Sort by field number, like task, for now. That changes in
   1.5." He clarified: "Just the field's number". For the new-ticket form he
   chose "Keep the levels", and for the ticket page "Group it too".
2. **Bugs 1–4:** all fixed in 1.3.0, each as its own PR before A. For bug 4,
   "Show once".
3. **The new calls:** "Browser session only". Keeping the existing routes as
   they are is a proposal, not his decision.
4. **Deactivation:** hidden from new tickets and from the list, still shown
   where it holds data, with Show inactive and Reactivate.
5. **Retiring an option** works the same way as deactivating a field.
6. **Ticket creation with a dropped value:** "Drop the value, but return a 20X
   that's appropriate?", then "201 + warnings list".
7. **Deleting an option:** "also allow delete, but the delete action, like the
   addition and retirement is audited", then "and the delete action is only
   valid if that option is unused". Renaming keeps the earlier proposal: the
   old text is retired and the new text is added.
8. **Audit:** "Every field change".
9. **Field type:** "Locked everywhere".
10. **Field name, option text and path in the audit view:** "Show them".
11. **Attachments removed by a node delete:** audited as
    `custom_field_detached`, "Yes, same transaction".
12. **Number fields:** "Validate now".
13. **A field that holds a value but is no longer attached:** "After item,
    read-only".

### Stated defaults (not Erik's decisions)

These are the design's choices where Erik gave no separate answer. Each can
be changed later without reworking the plan.

- **`/usage` is browser-session only**, following his rule for the other two
  new calls.
- **What counts as a number:** the trimmed value must parse with
  `strconv.ParseFloat`, and must not be NaN, infinite, out of range or
  hexadecimal. It is stored as written (§3).
- **Requesters also receive `warnings`** on their own create responses (§3).
- **Editing an unused option in place** is allowed. A rename of an option
  that tickets hold is a retire plus an add (§4).
- **The website PR** targets `docs/web-1-3-0`, not `v1.3.0-beta`, because the
  site lives on its own branch (§10, PR-8).
- **Bug PRs leave `docs/api/openapi.yaml`, `DESIGN.md` and the upgrade notes
  alone.** PR-5 and PR-7 document everything, so each of those files has
  exactly one PR that edits it.

— Opus 5.5
