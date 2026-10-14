-- custom_field_assignments.scope_id has no foreign key, so deleting a category,
-- type or item used to leave its assignments pointing at nothing (#419).
-- Deletes now remove them with the node; this clears the ones already left.
DELETE FROM custom_field_assignments a
WHERE (a.scope_type = 'category' AND NOT EXISTS (SELECT 1 FROM categories c WHERE c.id = a.scope_id))
   OR (a.scope_type = 'type' AND NOT EXISTS (SELECT 1 FROM types t WHERE t.id = a.scope_id))
   OR (a.scope_type = 'item' AND NOT EXISTS (SELECT 1 FROM items i WHERE i.id = a.scope_id));
