#!/usr/bin/env bash
# Runs inside the container (see Dockerfile and docker.sh). Starts PostgreSQL,
# makes sure frontend/node_modules matches package-lock.json, then runs
# scripts/screenshots.sh with the arguments it was given.
set -euo pipefail

cd "$GHD_REPO"

pg_ctlcluster 16 main start
for _ in $(seq 1 30); do
  pg_isready -q -h /var/run/postgresql && break
  sleep 1
done
su postgres -c "psql -qX -v ON_ERROR_STOP=1 -c \"CREATE ROLE helpdesk SUPERUSER LOGIN PASSWORD 'helpdesk'\"" >/dev/null

# node_modules is a named volume (the host's would hold macOS binaries), so
# screenshots.sh always finds the directory and never installs. Install here
# whenever the lock file changed since the volume was filled.
fe=frontend
want="$(sha256sum "$fe/package-lock.json" | cut -d' ' -f1)"
if [ "$(cat "$fe/node_modules/.ghd-lock-sha256" 2>/dev/null || true)" != "$want" ]; then
  echo "==> npm ci (node_modules volume is empty or out of date)"
  find "$fe/node_modules" -mindepth 1 -delete
  (cd "$fe" && npm ci)
  echo "$want" > "$fe/node_modules/.ghd-lock-sha256"
fi

exec ./scripts/screenshots.sh "$@"
