#!/usr/bin/env bash
#
# Runs scripts/screenshots.sh inside a Linux container, for a machine where the
# script itself refuses to run (macOS). Arguments are passed through:
#
#   scripts/screenshots/docker.sh                    full run
#   scripts/screenshots/docker.sh --only 05,12       some shots
#   scripts/screenshots/docker.sh --publish DIR      also copy the shots into DIR/screenshots/
#   scripts/screenshots/docker.sh --help
#
# --publish is the one argument this script reads: DIR is taken relative to the
# directory you run this from, may be anywhere on the host (it is mounted into
# the container at /publish), and the script inside is told /publish.
#
# This targets Docker Desktop on macOS, where files the container creates in the
# mounted checkout and output directory belong to you. On a Linux host the
# container runs as root, so frontend/dist, backend/internal/ui/dist and the
# output directory would be root-owned; run screenshots.sh itself there instead.
#
# Environment:
#   SHOTS_HOST_OUT        host directory that receives the output (png/,
#                         manifest.txt, report.txt, the logs) [a new mktemp
#                         directory, printed at the end]
#   GHD_SHOTS_REBUILD     1 rebuilds the image even though nothing it is built
#                         from changed (the Dockerfile, entrypoint.sh and the Go
#                         version in go.mod are checked on every run; this is for
#                         what they cannot see, such as newer apt packages)
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
# still resolves. The container can change the working tree but not the
# repository: the git directory (and, for a linked worktree, the main
# repository's) is mounted read-only. screenshots.sh restores
# backend/internal/ui/dist/index.html with git checkout, which needs a writable
# index; entrypoint.sh gives git a copy of it in /tmp (GIT_INDEX_FILE), so the
# host's index is left as it was.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
image=ghd-shots-env
name=ghd-shots-run

command -v docker >/dev/null || { echo "error: docker is not on PATH" >&2; exit 1; }

go_version="$(awk '$1 == "go" { print $2; exit }' "$root/backend/go.mod")"
[ -n "$go_version" ] || { echo "error: no go directive in backend/go.mod" >&2; exit 1; }

# The image is rebuilt when what it is built from changed: a label on the image
# holds a hash of the Dockerfile, entrypoint.sh and the Go version.
sha() { if command -v sha256sum >/dev/null; then sha256sum; else shasum -a 256; fi; }
want="$({ cat "$root/scripts/screenshots/Dockerfile" "$root/scripts/screenshots/entrypoint.sh"; echo "go $go_version"; } | sha | cut -d' ' -f1)"
have="$(docker image inspect -f '{{index .Config.Labels "ghd.shots.hash"}}' "$image" 2>/dev/null || true)"
if [ "${GHD_SHOTS_REBUILD:-0}" = 1 ] || [ "$have" != "$want" ]; then
  echo "==> building $image (Go $go_version)"
  docker build -t "$image" --label "ghd.shots.hash=$want" --build-arg "GO_VERSION=$go_version" -f "$root/scripts/screenshots/Dockerfile" "$root/scripts/screenshots"
fi

out="${SHOTS_HOST_OUT:-$(mktemp -d "${TMPDIR:-/tmp}/ghd-shots-out.XXXXXX")}"
mkdir -p "$out"
# Absolute: docker -v reads a bare name as a named volume.
out="$(cd "$out" && pwd)"

mounts=(-v "$root:$root" -v "ghd-shots-node-modules:$root/frontend/node_modules" -v ghd-shots-cache:/cache -v "$out:/out")

# The repository is read-only inside the container. An ordinary checkout has its
# .git inside $root; a linked worktree has a .git file naming a directory in the
# main repository, and the container needs both to resolve. Asking git (rather
# than reading the .git file) copes with relative and absolute paths alike.
gitdir="$(git -C "$root" rev-parse --absolute-git-dir)"
common="$(git -C "$root" rev-parse --path-format=absolute --git-common-dir)"
mounts+=(-v "$common:$common:ro")
if [ "$gitdir" != "$common" ]; then mounts+=(-v "$gitdir:$gitdir:ro"); fi

# --publish DIR: the one argument whose value is a path on the host.
args=()
publish=""
while [ $# -gt 0 ]; do
  case "$1" in
    --publish)
      [ $# -ge 2 ] || { echo "error: --publish needs a directory" >&2; exit 1; }
      publish="$2"; shift 2 ;;
    --publish=*) publish="${1#--publish=}"; shift ;;
    *) args+=("$1"); shift ;;
  esac
done
if [ -n "$publish" ]; then
  [ -d "$publish" ] || { echo "error: --publish: $publish is not a directory" >&2; exit 1; }
  publish="$(cd "$publish" && pwd)"
  mounts+=(-v "$publish:/publish")
  args+=(--publish /publish)
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
  "$image" ${args[@]+"${args[@]}"} || status=$?
echo "output: $out"
exit "$status"
