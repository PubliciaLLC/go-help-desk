-- An OAuth client acts through a service user (#420, decisions on #434).
--
-- A service user is an ordinary users row with one extra fact, and a rule the
-- database enforces so that no code path has to remember it: it can never be
-- an administrator and it can never hold anything that would let somebody
-- sign in as it. Handlers check first so a caller gets a 4xx with a reason;
-- this constraint is the backstop, not the first line.
ALTER TABLE users ADD COLUMN service_account BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE users ADD CONSTRAINT users_service_account_inert CHECK (
    NOT service_account OR (
        role <> 'admin'
        AND password_hash = ''
        AND mfa_secret = '' AND mfa_enabled = FALSE
        AND saml_subject = '' AND oidc_subject = ''
    )
);

ALTER TABLE oauth_clients ADD COLUMN user_id UUID;

-- One service user per existing client, named after it, staff role, no groups,
-- no way to sign in. The address is derived from the client's id so the join
-- back is exact. ".invalid" is reserved (RFC 2606) and can never receive mail.
-- Only clients without a user are touched, so running this again adds nothing.
WITH made AS (
    INSERT INTO users (id, email, display_name, role, service_account)
    SELECT gen_random_uuid(), 'oauth-' || c.id || '@service-account.invalid', c.name, 'staff', TRUE
    FROM oauth_clients c
    WHERE c.user_id IS NULL
    RETURNING id, email
)
UPDATE oauth_clients c
SET user_id = made.id
FROM made
WHERE made.email = 'oauth-' || c.id || '@service-account.invalid';

ALTER TABLE oauth_clients ALTER COLUMN user_id SET NOT NULL;

-- UNIQUE: one service user per client, never shared. RESTRICT: a client is
-- deleted first and its user is then soft-deleted, so the row is never
-- hard-deleted from under a client.
ALTER TABLE oauth_clients ADD CONSTRAINT oauth_clients_user_id_key UNIQUE (user_id);
ALTER TABLE oauth_clients ADD CONSTRAINT oauth_clients_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT;
