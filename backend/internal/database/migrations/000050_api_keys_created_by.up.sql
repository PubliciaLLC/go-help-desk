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
ALTER TABLE api_keys
    ADD COLUMN created_by UUID REFERENCES users(id) ON DELETE SET NULL;
