-- Site icon (#708): one row of metadata and up to eight files (master.png and
-- the seven the server generates). See domain/siteicon.

-- name: GetSiteIcon :one
SELECT background, version, source_size, source_sha256, updated_at FROM site_icon WHERE id;

-- name: GetSiteIconFile :one
SELECT f.data, i.version FROM site_icon_files f JOIN site_icon i ON i.id = f.icon_id WHERE f.name = $1;

-- name: ListSiteIconFileSizes :many
SELECT name, octet_length(data)::int AS bytes FROM site_icon_files WHERE name <> 'master.png';

-- name: LockSiteIcon :one
SELECT background, version, source_size, source_sha256, updated_at FROM site_icon WHERE id FOR UPDATE;

-- name: UpsertSiteIcon :exec
INSERT INTO site_icon (id, background, version, source_size, source_sha256, updated_at)
VALUES (TRUE, $1, $2, $3, $4, now())
ON CONFLICT (id) DO UPDATE SET background = EXCLUDED.background, version = EXCLUDED.version,
  source_size = EXCLUDED.source_size, source_sha256 = EXCLUDED.source_sha256, updated_at = now();

-- name: UpsertSiteIconFile :exec
INSERT INTO site_icon_files (name, data) VALUES ($1, $2)
ON CONFLICT (name) DO UPDATE SET data = EXCLUDED.data;

-- name: DeleteSiteIcon :execrows
DELETE FROM site_icon WHERE id;
