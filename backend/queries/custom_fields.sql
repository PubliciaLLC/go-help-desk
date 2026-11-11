-- ── Field definitions ─────────────────────────────────────────────────────────

-- name: CreateCustomFieldDef :one
INSERT INTO custom_field_defs (id, name, field_type, options, retired_options, sort_order, active)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListCustomFieldDefs :many
-- place_count is how many places the field is attached to, counted in this one
-- query so the admin list does not ask once per field. It counts exactly what
-- ListAssignmentsForFieldDef returns: an attachment whose category, type or item
-- is gone (scope_id has no foreign key) is not a place, so it is not counted.
WITH placed AS (
    SELECT a.field_def_id, count(*) AS places
    FROM custom_field_assignments a
    LEFT JOIN items i ON a.scope_type = 'item' AND i.id = a.scope_id
    LEFT JOIN types t ON t.id = CASE a.scope_type WHEN 'type' THEN a.scope_id WHEN 'item' THEN i.type_id END
    JOIN categories c ON c.id = CASE a.scope_type WHEN 'category' THEN a.scope_id ELSE t.category_id END
    GROUP BY a.field_def_id
)
SELECT d.*, COALESCE(p.places, 0)::int AS place_count
FROM custom_field_defs d
LEFT JOIN placed p ON p.field_def_id = d.id
ORDER BY d.sort_order, d.name;

-- name: GetCustomFieldDef :one
SELECT * FROM custom_field_defs WHERE id = $1;

-- Takes a shared row lock on each named definition, in id order, held until the
-- transaction ends. A value write holds it while it checks the definition, so a
-- change that takes the row FOR UPDATE (deactivate, retire an option) waits for
-- the write instead of landing between its check and its insert. Sorted so two
-- writers naming the same fields lock them in the same order and cannot deadlock.
-- name: LockCustomFieldDefsForShare :many
SELECT * FROM custom_field_defs
WHERE id = ANY(sqlc.arg(ids)::uuid[])
ORDER BY id
FOR SHARE;

-- The exclusive side of the lock above. An admin change takes the definition
-- FOR UPDATE and so waits for every value write that holds it FOR SHARE, and
-- blocks every one that starts after, until it commits. What it checks (is the
-- option held, does any ticket have a value) is therefore still true when it
-- writes.
-- name: LockCustomFieldDefForUpdate :one
SELECT * FROM custom_field_defs WHERE id = $1 FOR UPDATE;

-- Every definition, locked, in id order (the order LockCustomFieldDefsForShare
-- takes them in). Reordering rewrites all the numbers, so it holds them all.
-- name: LockAllCustomFieldDefsForUpdate :many
SELECT * FROM custom_field_defs ORDER BY id FOR UPDATE;

-- name: UpdateCustomFieldDef :exec
UPDATE custom_field_defs
SET name = $2, field_type = $3, options = $4, retired_options = $5, sort_order = $6, active = $7
WHERE id = $1;

-- name: ReorderCustomFieldDefs :exec
-- Position n (from 1) becomes the field's number. Only rows whose number
-- changes are written.
UPDATE custom_field_defs d SET sort_order = o.ord::int
FROM unnest(sqlc.arg(ids)::uuid[]) WITH ORDINALITY AS o(id, ord)
WHERE d.id = o.id AND d.sort_order <> o.ord::int;

-- ── Usage ─────────────────────────────────────────────────────────────────────

-- name: AnyCustomFieldValue :one
SELECT EXISTS (SELECT 1 FROM ticket_custom_field_values WHERE field_def_id = $1) AS held;

-- name: FirstHeldCustomFieldValue :one
-- One value from the list that some ticket holds; no row when none does.
SELECT value FROM ticket_custom_field_values
WHERE field_def_id = $1 AND value = ANY(sqlc.arg(candidates)::text[])
LIMIT 1;

-- name: CountCustomFieldValues :one
SELECT count(*) FROM ticket_custom_field_values WHERE field_def_id = $1;

-- name: CountCustomFieldValuesByValue :many
SELECT value, count(*)::bigint AS tickets
FROM ticket_custom_field_values WHERE field_def_id = $1
GROUP BY value;

-- ── Assignments ───────────────────────────────────────────────────────────────

-- name: CreateCustomFieldAssignment :one
INSERT INTO custom_field_assignments (id, field_def_id, scope_type, scope_id, sort_order, visible_on_new, required_on_new, visible_to_guests)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListAssignmentsForScope :many
SELECT
    a.id,
    a.field_def_id,
    a.scope_type,
    a.scope_id,
    a.sort_order,
    a.visible_on_new,
    a.required_on_new,
    a.visible_to_guests,
    d.name       AS field_name,
    d.field_type AS field_type,
    d.options    AS field_options,
    d.retired_options AS field_retired_options,
    d.sort_order AS field_sort_order,
    d.active     AS field_active
FROM custom_field_assignments a
JOIN custom_field_defs d ON a.field_def_id = d.id
WHERE a.scope_type = $1 AND a.scope_id = $2
ORDER BY d.sort_order, d.name;

-- name: ListAssignmentsForFieldDef :many
-- Where a field is attached, in tree order: category by number and name, and
-- under each the category's own attachment first, then each type's, each followed
-- by its items'. The category join is an inner join on purpose: an attachment
-- whose node is gone (scope_id has no foreign key) has no category and is not
-- returned. ListCustomFieldDefs counts places with the same joins.
SELECT
    a.id,
    a.field_def_id,
    a.scope_type,
    a.scope_id,
    a.sort_order,
    a.visible_on_new,
    a.required_on_new,
    a.visible_to_guests,
    c.id     AS category_id,
    c.name   AS category_name,
    c.active AS category_active,
    t.id     AS type_id,
    t.name   AS type_name,
    t.active AS type_active,
    i.id     AS item_id,
    i.name   AS item_name,
    i.active AS item_active
FROM custom_field_assignments a
LEFT JOIN items i ON a.scope_type = 'item' AND i.id = a.scope_id
LEFT JOIN types t ON t.id = CASE a.scope_type WHEN 'type' THEN a.scope_id WHEN 'item' THEN i.type_id END
JOIN categories c ON c.id = CASE a.scope_type WHEN 'category' THEN a.scope_id ELSE t.category_id END
WHERE a.field_def_id = $1
ORDER BY c.sort_order, c.name, c.id,
         t.sort_order NULLS FIRST, t.name NULLS FIRST, t.id NULLS FIRST,
         i.sort_order NULLS FIRST, i.name NULLS FIRST, i.id NULLS FIRST;

-- name: GetScopeChain :one
-- The category, type and item a node sits under, NULL below the node's own level
-- and everywhere for a node that does not exist. Read with ListAssignmentsForFieldDef
-- to find an attachment of the same field above or below a new one.
SELECT c.id AS category_id, t.id AS type_id, i.id AS item_id
FROM (SELECT sqlc.arg(scope_type)::text AS st, sqlc.arg(scope_id)::uuid AS sid) s
LEFT JOIN items i ON s.st = 'item' AND i.id = s.sid
LEFT JOIN types t ON t.id = CASE s.st WHEN 'type' THEN s.sid WHEN 'item' THEN i.type_id END
LEFT JOIN categories c ON c.id = CASE s.st WHEN 'category' THEN s.sid ELSE t.category_id END;

-- name: GetAssignmentScopePath :one
-- The path of a node as the audit log records it: "Hardware", "Hardware › Laptop",
-- "Hardware › Laptop › Dell". A snapshot, so an entry still reads correctly after
-- a rename or a delete. The node-delete statements in categories.sql build the
-- same text.
SELECT COALESCE(CASE sqlc.arg(scope_type)::text
    WHEN 'category' THEN (SELECT c.name FROM categories c WHERE c.id = sqlc.arg(scope_id)::uuid)
    WHEN 'type' THEN (SELECT c.name || ' › ' || t.name
                      FROM types t JOIN categories c ON c.id = t.category_id WHERE t.id = sqlc.arg(scope_id)::uuid)
    WHEN 'item' THEN (SELECT c.name || ' › ' || t.name || ' › ' || i.name
                      FROM items i JOIN types t ON t.id = i.type_id JOIN categories c ON c.id = t.category_id
                      WHERE i.id = sqlc.arg(scope_id)::uuid)
END, '')::text AS path;

-- name: GetCustomFieldAssignment :one
SELECT * FROM custom_field_assignments WHERE id = $1;

-- name: GetCustomFieldAssignmentForUpdate :one
-- Locked, so the audit entry's "before" is the row this transaction then writes.
SELECT * FROM custom_field_assignments WHERE id = $1 FOR UPDATE;

-- name: UpdateCustomFieldAssignment :exec
UPDATE custom_field_assignments
SET sort_order = $2, visible_on_new = $3, required_on_new = $4, visible_to_guests = $5
WHERE id = $1;

-- name: DeleteCustomFieldAssignment :execrows
DELETE FROM custom_field_assignments WHERE id = $1;

-- ── Values ────────────────────────────────────────────────────────────────────

-- name: UpsertCustomFieldValue :exec
INSERT INTO ticket_custom_field_values (ticket_id, field_def_id, value, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (ticket_id, field_def_id)
DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- name: DeleteCustomFieldValue :exec
DELETE FROM ticket_custom_field_values
WHERE ticket_id = $1 AND field_def_id = $2;

-- name: ListCustomFieldValuesForTicket :many
SELECT
    v.ticket_id,
    v.field_def_id,
    d.name       AS field_name,
    d.field_type AS field_type,
    d.options    AS field_options,
    d.retired_options AS field_retired_options,
    v.value,
    v.updated_at
FROM ticket_custom_field_values v
JOIN custom_field_defs d ON v.field_def_id = d.id
WHERE v.ticket_id = $1
ORDER BY d.sort_order, d.name;
