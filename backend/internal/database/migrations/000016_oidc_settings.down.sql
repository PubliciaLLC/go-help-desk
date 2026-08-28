DELETE FROM settings
WHERE key IN (
  'oidc_enabled',
  'oidc_issuer_url',
  'oidc_client_id',
  'oidc_client_secret',
  'oidc_redirect_url'
);
