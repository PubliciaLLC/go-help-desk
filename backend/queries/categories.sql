-- name: CreateCategory :exec
INSERT INTO categories (id, name, sort_order, active) VALUES ($1, $2, $3, $4);

-- name: GetCategory :one
SELECT * FROM categories WHERE id = $1;

-- name: UpdateCategory :exec
UPDATE categories SET name = $2, sort_order = $3, active = $4 WHERE id = $1;

-- name: DeleteCategory :exec
DELETE FROM categories WHERE id = $1;

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

-- name: DeleteType :exec
DELETE FROM types WHERE id = $1;

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

-- name: DeleteItem :exec
DELETE FROM items WHERE id = $1;

-- name: ListItems :many
SELECT * FROM items
WHERE type_id = $1 AND ($2::boolean = FALSE OR active = TRUE)
ORDER BY sort_order, name;
