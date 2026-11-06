-- Who issued an API key (#450). Until now a key was always made by its owner,
-- so nothing recorded it. With staff creating their own keys and
-- administrators issuing keys on someone's behalf, "who made this" is no
-- longer the same as "whose is this", and the owner of a key they did not
-- make has a right to see that.
--
-- New keys record their issuer, the owner included when they made it
-- themselves; created_by <> user_id is what "issued on behalf" means. NULL
-- means the issuer is not known: every key made before this migration (the
-- owner made all of them), or one whose issuer was deleted afterwards.
--
-- IF NOT EXISTS: this migration was first numbered 000048 and a development
-- database that ran it under that number already has the column.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS created_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- The API key that created a webhook or last re-pointed its URL (#450); NULL
-- for one made or last re-pointed in a signed-in session, and for every hook
-- that exists today. A hook tied to a key stops firing when that key is deleted
-- or has expired, judged from the key's own row at delivery time. So this is
-- deliberately NOT a foreign key: deleting the key must not null the column,
-- because "the key is gone" is exactly what pauses the hook. Only a signed-in
-- administrator re-approving the hook clears it.
ALTER TABLE webhook_configs
    ADD COLUMN IF NOT EXISTS api_key_id UUID;
