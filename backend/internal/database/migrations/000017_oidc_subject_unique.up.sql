-- oidc_subject holds the OIDC "sub" claim; '' means "not federated via OIDC",
-- which every local account stores. The uniqueness constraint is therefore
-- partial, mirroring users_saml_subject_idx in 000001_init.
CREATE UNIQUE INDEX users_oidc_subject_idx ON users (oidc_subject) WHERE oidc_subject != '';
