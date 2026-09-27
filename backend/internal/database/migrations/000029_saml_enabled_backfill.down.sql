-- Not reversible in the sense of recovering the exact prior value: a plain
-- 'false' here would silently disable every SAML instance this migration's
-- up side just preserved, which is the one outcome this exists to prevent.
-- Down leaves saml_enabled as-is; rolling back the migration framework's
-- bookkeeping does not need to also re-break working installs.
SELECT 1;
