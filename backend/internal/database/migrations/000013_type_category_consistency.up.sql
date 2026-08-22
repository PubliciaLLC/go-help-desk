-- Ensure a type_id always belongs to its accompanying category_id, wherever
-- both are stored together (canned_responses, group_scopes, tickets). A row
-- with type_id set but naming the wrong category previously passed both the
-- single-column FKs and each table's own scope CHECK constraint, producing a
-- silently unreachable/inconsistent state (see canned_responses' scope
-- filter, group_scopes' scoping, and ticket CTI assignment).
--
-- types.id is already globally unique, so (category_id, id) is trivially
-- unique too — this constraint just gives Postgres a composite key to check
-- cross-column FKs against. FK MATCH SIMPLE (the default) skips the check
-- whenever either referencing column is NULL, so global/whole-category rows
-- (type_id NULL) are unaffected.

ALTER TABLE types ADD CONSTRAINT types_category_id_id_key UNIQUE (category_id, id);

ALTER TABLE canned_responses ADD CONSTRAINT canned_responses_type_category_fk
    FOREIGN KEY (category_id, type_id) REFERENCES types (category_id, id);

ALTER TABLE group_scopes ADD CONSTRAINT group_scopes_type_category_fk
    FOREIGN KEY (category_id, type_id) REFERENCES types (category_id, id);

ALTER TABLE tickets ADD CONSTRAINT tickets_type_category_fk
    FOREIGN KEY (category_id, type_id) REFERENCES types (category_id, id);
