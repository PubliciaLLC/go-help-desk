-- Add SAML SP configuration rows to the settings table.
-- ON CONFLICT DO NOTHING is safe to run against an existing DB.
INSERT INTO settings (key, value) VALUES
    ('saml_metadata_url', '""'),
    ('saml_cert_pem',     '""'),
    ('saml_key_pem',      '""')
ON CONFLICT (key) DO NOTHING;
