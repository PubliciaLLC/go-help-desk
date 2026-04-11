-- name: CreateGroup :exec
INSERT INTO groups (id, name, description) VALUES ($1, $2, $3);

-- name: GetGroup :one
SELECT * FROM groups WHERE id = $1;

-- name: UpdateGroup :exec
UPDATE groups SET name = $2, description = $3 WHERE id = $1;

-- name: DeleteGroup :exec
DELETE FROM groups WHERE id = $1;

-- name: ListGroups :many
SELECT * FROM groups ORDER BY name;

-- name: AddGroupMember :exec
INSERT INTO group_members (group_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: RemoveGroupMember :exec
DELETE FROM group_members WHERE group_id = $1 AND user_id = $2;

-- name: ListGroupMembers :many
SELECT user_id FROM group_members WHERE group_id = $1;

-- name: ListGroupsForUser :many
SELECT g.* FROM groups g
JOIN group_members gm ON g.id = gm.group_id
WHERE gm.user_id = $1
ORDER BY g.name;

-- name: AddGroupScope :exec
INSERT INTO group_scopes (group_id, category_id, type_id) VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: RemoveGroupScope :exec
DELETE FROM group_scopes
WHERE group_id = $1 AND category_id = $2
AND (type_id = $3 OR (type_id IS NULL AND $3::uuid IS NULL));

-- name: ListGroupScopes :many
SELECT * FROM group_scopes WHERE group_id = $1;

-- name: ListGroupsInScope :many
SELECT DISTINCT g.* FROM groups g
JOIN group_scopes gs ON g.id = gs.group_id
WHERE gs.category_id = $1
AND (gs.type_id IS NULL OR gs.type_id = $2)
ORDER BY g.name;
