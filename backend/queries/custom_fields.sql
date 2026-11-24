-- ── Field definitions ─────────────────────────────────────────────────────────

-- name: CreateCustomFieldDef :one
INSERT INTO custom_field_defs (id, name, field_type, options, sort_order, active)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListCustomFieldDefs :many
SELECT * FROM custom_field_defs ORDER BY sort_order, name;

-- name: GetCustomFieldDef :one
SELECT * FROM custom_field_defs WHERE id = $1;

-- name: UpdateCustomFieldDef :exec
UPDATE custom_field_defs
SET name = $2, field_type = $3, options = $4, sort_order = $5, active = $6
WHERE id = $1;

-- ── Assignments ───────────────────────────────────────────────────────────────

-- name: CreateCustomFieldAssignment :one
INSERT INTO custom_field_assignments (id, field_def_id, scope_type, scope_id, sort_order, visible_on_new, required_on_new)
VALUES ($1, $2, $3, $4, $5, $6, $7)
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
    d.name       AS field_name,
    d.field_type AS field_type,
    d.options    AS field_options,
    d.active     AS field_active
FROM custom_field_assignments a
JOIN custom_field_defs d ON a.field_def_id = d.id
WHERE a.scope_type = $1 AND a.scope_id = $2
ORDER BY a.sort_order;

-- name: GetCustomFieldAssignment :one
SELECT * FROM custom_field_assignments WHERE id = $1;

-- name: UpdateCustomFieldAssignment :exec
UPDATE custom_field_assignments
SET sort_order = $2, visible_on_new = $3, required_on_new = $4
WHERE id = $1;

-- name: DeleteCustomFieldAssignment :exec
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
    v.value,
    v.updated_at
FROM ticket_custom_field_values v
JOIN custom_field_defs d ON v.field_def_id = d.id
WHERE v.ticket_id = $1
ORDER BY d.sort_order, d.name;
