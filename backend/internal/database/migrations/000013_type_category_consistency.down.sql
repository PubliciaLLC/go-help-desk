ALTER TABLE tickets DROP CONSTRAINT tickets_type_category_fk;
ALTER TABLE group_scopes DROP CONSTRAINT group_scopes_type_category_fk;
ALTER TABLE canned_responses DROP CONSTRAINT canned_responses_type_category_fk;
ALTER TABLE types DROP CONSTRAINT types_category_id_id_key;
