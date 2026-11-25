-- Site icon (#708): one uploaded image, kept as a 512 px master and the seven
-- files the server generated from it. Empty until an administrator uploads one;
-- an empty table means the built-in icon, exactly as before this migration.
CREATE TABLE site_icon (
    id            BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (id),
    background    TEXT        NOT NULL CHECK (background ~ '^#[0-9a-f]{6}$'),
    version       TEXT        NOT NULL CHECK (version ~ '^[0-9a-f]{16}$'),
    source_size   INTEGER     NOT NULL CHECK (source_size BETWEEN 512 AND 4096),
    source_sha256 TEXT        NOT NULL CHECK (source_sha256 ~ '^[0-9a-f]{64}$'),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE site_icon_files (
    name    TEXT    PRIMARY KEY CHECK (name IN (
                'master.png', 'favicon.ico', 'favicon-16.png', 'favicon-32.png',
                'apple-touch-icon.png', 'icon-192.png', 'icon-512.png',
                'icon-maskable-512.png')),
    icon_id BOOLEAN NOT NULL DEFAULT TRUE REFERENCES site_icon (id) ON DELETE CASCADE,
    data    BYTEA   NOT NULL
);
