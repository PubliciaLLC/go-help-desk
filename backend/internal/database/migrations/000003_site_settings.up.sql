-- Seed branding settings with empty defaults so the keys exist in the table.
INSERT INTO settings (key, value) VALUES
    ('site_name',     '"Open Help Desk"'),
    ('site_logo_url', '""')
ON CONFLICT (key) DO NOTHING;
