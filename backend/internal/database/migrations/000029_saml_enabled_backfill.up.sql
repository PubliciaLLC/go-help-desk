-- saml_enabled has been stored since 000001, seeded to 'false' for every
-- instance and never consulted anywhere: reloadSAML has only ever gated on
-- the three config fields being non-empty. #300 makes it a real gate,
-- matching how oidc_enabled already governs OIDC — closing an existing
-- frontend/backend mismatch (the "Enable SAML login" toggle has always
-- written this key, doing nothing) and giving SAML the same explicit
-- disable this PR's stranding guard needs to check.
--
-- Making it a real gate on an unconditional 'false' default would silently
-- disable every SAML deployment that has been relying purely on "the three
-- fields are present" — which is every one of them, since that was the only
-- rule that ever existed. This backfills 'true' for exactly that set: an
-- instance whose SAML is already fully configured keeps working after this
-- migration lands, whether it was configured through the settings page's own
-- toggle (which already writes this key, so this is a no-op there) or
-- directly against the API without ever touching the toggle.
UPDATE settings
SET value = 'true'::jsonb
WHERE key = 'saml_enabled'
  AND value = 'false'::jsonb
  AND EXISTS (
    SELECT 1 FROM settings s2 WHERE s2.key = 'saml_metadata_url'
      AND s2.value <> '""'::jsonb AND s2.value <> 'null'::jsonb
  )
  AND EXISTS (
    SELECT 1 FROM settings s3 WHERE s3.key = 'saml_cert_pem'
      AND s3.value <> '""'::jsonb AND s3.value <> 'null'::jsonb
  )
  AND EXISTS (
    SELECT 1 FROM settings s4 WHERE s4.key = 'saml_key_pem'
      AND s4.value <> '""'::jsonb AND s4.value <> 'null'::jsonb
  );
