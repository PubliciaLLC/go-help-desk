-- name: CreateCategory :exec
INSERT INTO categories (id, name, sort_order, active) VALUES ($1, $2, $3, $4);

-- name: GetCategory :one
SELECT * FROM categories WHERE id = $1;

-- name: UpdateCategory :exec
UPDATE categories SET name = $2, sort_order = $3, active = $4 WHERE id = $1;

-- name: DeleteCategory :execrows
-- Takes the custom-field assignments of the category, of its types and of
-- their items with it (#419). custom_field_assignments.scope_id has no foreign
-- key, so they were left pointing at nothing. One statement, so the assignments
-- and the node go together or not at all: if the delete is refused (a ticket
-- arrived meanwhile), the assignments stay.
WITH gone AS (
    DELETE FROM custom_field_assignments
    WHERE (scope_type = 'category' AND scope_id = $1)
       OR (scope_type = 'type' AND scope_id IN (SELECT t.id FROM types t WHERE t.category_id = $1))
       OR (scope_type = 'item' AND scope_id IN (
            SELECT i.id FROM items i JOIN types t ON t.id = i.type_id WHERE t.category_id = $1))
)
DELETE FROM categories WHERE categories.id = $1;

-- name: ListCategories :many
SELECT * FROM categories
WHERE ($1::boolean = FALSE OR active = TRUE)
ORDER BY sort_order, name;

-- name: CreateType :exec
INSERT INTO types (id, category_id, name, sort_order, active) VALUES ($1, $2, $3, $4, $5);

-- name: GetType :one
SELECT * FROM types WHERE id = $1;

-- name: UpdateType :exec
UPDATE types SET category_id = $2, name = $3, sort_order = $4, active = $5 WHERE id = $1;

-- name: DeleteType :execrows
-- As DeleteCategory: the type's own and its items' field assignments go in the
-- same statement.
WITH gone AS (
    DELETE FROM custom_field_assignments
    WHERE (scope_type = 'type' AND scope_id = $1)
       OR (scope_type = 'item' AND scope_id IN (SELECT i.id FROM items i WHERE i.type_id = $1))
)
DELETE FROM types WHERE types.id = $1;

-- name: ListTypes :many
SELECT * FROM types
WHERE category_id = $1 AND ($2::boolean = FALSE OR active = TRUE)
ORDER BY sort_order, name;

-- name: CreateItem :exec
INSERT INTO items (id, type_id, name, sort_order, active) VALUES ($1, $2, $3, $4, $5);

-- name: GetItem :one
SELECT * FROM items WHERE id = $1;

-- name: UpdateItem :exec
UPDATE items SET type_id = $2, name = $3, sort_order = $4, active = $5 WHERE id = $1;

-- name: DeleteItem :execrows
-- As DeleteCategory: the item's field assignments go in the same statement.
WITH gone AS (
    DELETE FROM custom_field_assignments WHERE scope_type = 'item' AND scope_id = $1
)
DELETE FROM items WHERE items.id = $1;

-- name: ListItems :many
SELECT * FROM items
WHERE type_id = $1 AND ($2::boolean = FALSE OR active = TRUE)
ORDER BY sort_order, name;

-- name: CountTicketsByCategory :one
-- tickets.category_id is ON DELETE RESTRICT. Counting first turns the raw
-- foreign-key 500 into a 409 that says how many tickets are in the way (#418).
SELECT COUNT(*) FROM tickets WHERE category_id = $1;

-- name: CountTicketsByType :one
-- A ticket that holds an item also holds that item's type, so this covers
-- the type's items too.
SELECT COUNT(*) FROM tickets WHERE type_id = $1;

-- name: CountTicketsByItem :one
SELECT COUNT(*) FROM tickets WHERE item_id = $1;
