ALTER TABLE webhook_configs DROP COLUMN IF EXISTS api_key_id;
ALTER TABLE api_keys DROP COLUMN IF EXISTS created_by;
