#!/usr/bin/env bash
#
# Runs scripts/screenshots.sh inside a Linux container, for a machine where the
# script itself refuses to run (macOS). Arguments are passed through:
#
#   scripts/screenshots/docker.sh                    full run
#   scripts/screenshots/docker.sh --only 05,12       some shots
#   scripts/screenshots/docker.sh --help
#
# Environment:
#   SHOTS_HOST_OUT        host directory that receives the output (png/,
#                         manifest.txt, report.txt, the logs) [a new mktemp
#                         directory, printed at the end]
#   GHD_SHOTS_REBUILD     1 rebuilds the image (after a go.mod or Dockerfile change)
#   SHOTS_*, FAKE_*       the settings documented in screenshots.sh are passed
#                         through, except SHOTS_OUT, which is /out in the container
#
# What it creates, all named ghd-shots-*: the image ghd-shots-env, the volumes
# ghd-shots-node-modules and ghd-shots-cache (Inter, Go and npm caches), and the
# container ghd-shots-run, which is removed when the run ends. No port is
# published: the server, the mail and scanner stand-ins and Postgres are
# reachable only inside the container.
#
# The checkout is mounted at its own host path, so a git worktree's .git file
# still resolves. The repository's .git directory is mounted read-only, except
# this worktree's own metadata, which the script needs to write (it restores
# backend/internal/ui/dist/index.html with git checkout).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
image=ghd-shots-env
name=ghd-shots-run

command -v docker >/dev/null || { echo "error: docker is not on PATH" >&2; exit 1; }

go_version="$(awk '$1 == "go" { print $2; exit }' "$root/backend/go.mod")"
[ -n "$go_version" ] || { echo "error: no go directive in backend/go.mod" >&2; exit 1; }

if [ "${GHD_SHOTS_REBUILD:-0}" = 1 ] || ! docker image inspect "$image" >/dev/null 2>&1; then
  echo "==> building $image (Go $go_version)"
  docker build -t "$image" --build-arg "GO_VERSION=$go_version" -f "$root/scripts/screenshots/Dockerfile" "$root/scripts/screenshots"
fi

out="${SHOTS_HOST_OUT:-$(mktemp -d "${TMPDIR:-/tmp}/ghd-shots-out.XXXXXX")}"
mkdir -p "$out"

mounts=(-v "$root:$root" -v "ghd-shots-node-modules:$root/frontend/node_modules" -v ghd-shots-cache:/cache -v "$out:/out")
# A linked worktree has a .git file that names a directory in the main
# repository. Make that directory resolve inside the container.
if [ -f "$root/.git" ]; then
  gitdir="$(sed -n 's/^gitdir: //p' "$root/.git")"
  common="$(cd "$gitdir/$(cat "$gitdir/commondir")" && pwd)"
  mounts+=(-v "$common:$common:ro" -v "$gitdir:$gitdir")
fi

# Pass through the SHOTS_* and FAKE_* settings the caller set.
envs=()
while IFS= read -r v; do
  envs+=(-e "$v")
done < <(compgen -e | grep -E '^(SHOTS_|FAKE_)' | grep -vE '^SHOTS_(HOST_OUT|OUT)$' || true)

docker rm -f "$name" >/dev/null 2>&1 || true
echo "==> running in container $name; output goes to $out"
status=0
docker run --rm --name "$name" --shm-size 1g \
  "${mounts[@]}" ${envs[@]+"${envs[@]}"} \
  -e "GHD_REPO=$root" -e SHOTS_OUT=/out -e XDG_CACHE_HOME=/cache \
  -e GOCACHE=/cache/go-build -e GOMODCACHE=/cache/go-mod -e npm_config_cache=/cache/npm \
  -e TMPDIR=/tmp -w "$root" \
  "$image" "$@" || status=$?
echo "output: $out"
exit "$status"
