#!/usr/bin/env bash
# Asserts that `docker compose up` leaves the attachments directory writable by
# the app, on a host where Docker created it as root.
#
# The bug: docker-compose.yml bind-mounts ./data/attachments into the app
# container, which runs as uid 100 / gid 101 (ghd, pinned in backend/Dockerfile).
# A bind mount does not take the image's ownership. On a fresh Linux host Docker
# creates the missing host directory as root:root, the app cannot write to it,
# and administrators see "Attachment uploads are disabled ... permission
# denied". Docker Desktop on macOS hides it, and an upgrade from 1.2 keeps the
# old owner, so only a fresh Linux install shows it, which is why nothing
# caught it.
#
# The fix is the `attachments-init` service in docker/docker-compose.yml. This
# script does not re-implement it: it reads that service out of
# `docker compose config`, runs its entrypoint and command with its user,
# capabilities and network mode through `docker run`, and checks what the app
# user can do afterwards. Edit the service and this follows it.
#
# The directory under test is a named volume, not a bind mount of a temp dir:
# Docker Desktop's file sharing does not honour chown, so a bind mount cannot
# show the bug there. A volume created by a root helper is root-owned on every
# host, which is the state a fresh Linux bind mount is in.
#
# Real Docker, no mocks. Needs docker, jq and the app image:
#
#     IMAGE=ghd:ci scripts/check-compose-attachments.sh
#
# With IMAGE unset it builds backend/Dockerfile (slow, needs buildx).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/.." && pwd)"
compose="$root/docker/docker-compose.yml"
build_override="$root/docker/docker-compose.build.yml"
dockerfile="$root/backend/Dockerfile"
fail=0

ok()   { printf '  ok   %s\n' "$1"; }
bad()  { printf '  FAIL %s\n' "$1"; fail=1; }
check() { # description, command...
  local desc="$1"; shift
  if "$@" >/dev/null 2>&1; then ok "$desc"; else bad "$desc"; fi
}

IMAGE="${IMAGE:-}"
if [ -z "$IMAGE" ]; then
  IMAGE=ghd-check-compose:local
  docker build -q -f "$dockerfile" -t "$IMAGE" "$root" >/dev/null
fi

# --- the service definition -------------------------------------------------

# SESSION_SECRET is required by the app service, so `config` needs one to
# render at all. The value is never used.
cfg() { SESSION_SECRET=x docker compose "$@" config --format json; }
svc="$(cfg -f "$compose")"

init="$(jq -c '.services["attachments-init"] // empty' <<<"$svc")"
if [ -z "$init" ]; then
  bad "docker-compose.yml has no attachments-init service"
  echo "FAIL"
  exit 1
fi

# The numbers the Dockerfile pins, read from it rather than typed in here, so
# a change to one without the other fails.
uid="$(sed -nE 's/.*adduser .* -u ([0-9]+) .*/\1/p' "$dockerfile")"
gid="$(sed -nE 's/.*addgroup .* -g ([0-9]+) .*/\1/p' "$dockerfile")"
[ -n "$uid" ] && [ -n "$gid" ] || { bad "cannot read uid/gid from $dockerfile"; echo FAIL; exit 1; }
owner="$uid:$gid"

echo "service definition"
check "the init service mentions the Dockerfile's owner $owner" \
  jq -e --arg o "$owner" '[.entrypoint[], (.command // [])[]] | any(contains($o))' <<<"$init"
check "the init service runs the same image as app" \
  jq -e --argjson s "$svc" '.image == $s.services.app.image' <<<"$init"
check "the init service runs as root" \
  jq -e '.user == "0" or .user == "root" or .user == "0:0"' <<<"$init"
check "restart is no" jq -e '.restart == "no"' <<<"$init"
check "no network" jq -e '.network_mode == "none"' <<<"$init"
check "every capability dropped" jq -e '.cap_drop | index("ALL")' <<<"$init"
check "no capability beyond the ones it needs" \
  jq -e '(.cap_add // []) - ["CHOWN","DAC_READ_SEARCH"] == []' <<<"$init"
check "it has no ports and nothing but the attachments mount" \
  jq -e '(.ports // []) == [] and ((.volumes // []) | map(.target) == ["/data/attachments"])' <<<"$init"
check "app waits for it to complete successfully" \
  jq -e '.services.app.depends_on["attachments-init"].condition == "service_completed_successfully"' <<<"$svc"
# docker-compose.build.yml gives app a build: section and the same image name.
# The init service needs no build of its own: `up --build` builds app before it
# creates any container, so the name resolves to the image just built (checked
# by hand with a tag that did not exist). What can be asserted here is that the
# two still share one name.
build_svc="$(cfg -f "$compose" -f "$build_override")"
check "under docker-compose.build.yml init and app are the same image" \
  jq -e '.services["attachments-init"].image == .services.app.image' <<<"$build_svc"
check "the Dockerfile's user really is $owner in the image" \
  test "$(docker run --rm --entrypoint sh "$IMAGE" -c 'echo $(id -u):$(id -g)')" = "$owner"

# --- run it -----------------------------------------------------------------

# docker run flags from the service, so what is tested is what compose runs.
# NUL-delimited, because the entrypoint script has newlines in it. (while-read
# rather than mapfile, which macOS's bash 3.2 does not have.)
flags=(); args=()
while IFS= read -r -d '' l; do flags+=("$l"); done < <(jq -j '
  ["--user", .user, "--network", .network_mode, "--entrypoint", .entrypoint[0]]
  + [(.cap_drop // [])[] | "--cap-drop=\(.)"]
  + [(.cap_add  // [])[] | "--cap-add=\(.)"]
  | .[] + "\u0000"' <<<"$init")
while IFS= read -r -d '' l; do args+=("$l"); done < <(jq -j '
  (.entrypoint[1:][], (.command // [])[]) | gsub("[$][$]"; "$") + "\u0000"' <<<"$init")
# (gsub: `config` prints the file's $$ escapes as they are, to be read back in
# by compose; docker run wants the single $ the container will see.)

vols=()
cleanup() { [ ${#vols[@]} -eq 0 ] || docker volume rm -f "${vols[@]}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# Sets $v, and is not called in $(...): the cleanup trap must see vols.
newvol() { v="ghd-check-$$-$1"; docker volume create "$v" >/dev/null; vols+=("$v"); }

# volume-nocopy matters. Without it Docker copies the image's own /data, owner
# included, into any empty volume on every mount, which would hand the app its
# directory back and hide the bug this exists to catch.
mnt() { echo "type=volume,src=$1,dst=$2,volume-nocopy"; }

# As root, with the capabilities of a default container: this is the stand-in
# for the Docker daemon (and for whoever made the directory), not the service.
as_root() { local v="$1" m="$2"; shift 2
  docker run --rm --user 0 --mount "$(mnt "$v" "$m")" --entrypoint sh "$IMAGE" -c "$1"; }

# Runs the service's own entrypoint and command against a volume mounted where
# the compose file mounts the bind: /data/attachments.
run_init() { docker run --rm "${flags[@]}" --mount "$(mnt "$1" "${2:-/data/attachments}")" "$IMAGE" "${args[@]}"; }

# As the app runs: read what is there, create a file and a directory, and
# remove them.
app_can_use() {
  docker run --rm --user "$owner" --mount "$(mnt "$1" /data/attachments)" --entrypoint sh "$IMAGE" -c '
    set -e
    find /data/attachments -type f -exec cat {} + >/dev/null
    echo x > /data/attachments/probe
    mkdir /data/attachments/probe.d
    echo x > /data/attachments/probe.d/f
    rm -r /data/attachments/probe /data/attachments/probe.d'
}

# The state a fresh Linux bind mount is in: empty, root:root, mode 755.
root_empty() { as_root "$1" /data/attachments 'chown 0:0 /data/attachments && chmod 755 /data/attachments'; }

echo "control: the bug, without the service"
newvol control; root_empty "$v"
if app_can_use "$v"; then
  bad "control: the app user could write to a root-owned directory, so the checks below prove nothing"
else
  ok "control: the app user cannot write to a root-owned directory"
fi

echo "root-owned and empty (a fresh Linux bind mount)"
newvol empty; root_empty "$v"
run_init "$v"
check "the app can read, write and remove" app_can_use "$v"
check "the directory is owned $owner" \
  test "$(as_root "$v" /data/attachments 'stat -c %u:%g /data/attachments')" = "$owner"

echo "missing (created by the init service, not by Docker)"
newvol missing
# Mount one level up, so /data/attachments does not exist yet.
run_init "$v" /data
check "the directory now exists" \
  test "$(as_root "$v" /data 'stat -c %u:%g /data/attachments')" = "$owner"
check "the app can use it (mounted at /data, as the app sees it)" \
  docker run --rm --user "$owner" --mount "$(mnt "$v" /data)" --entrypoint sh "$IMAGE" -c \
    'echo x > /data/attachments/probe && rm /data/attachments/probe'

echo "root-owned with files in it (a store written by something running as root)"
newvol files; root_empty "$v"
as_root "$v" /data/attachments '
  mkdir -p /data/attachments/ab/cd
  echo one > /data/attachments/ab/cd/file1; chmod 600 /data/attachments/ab/cd/file1
  echo two > /data/attachments/file2;       chmod 600 /data/attachments/file2
  chmod 700 /data/attachments/ab'
run_init "$v"
check "the app can read the files and write beside them" app_can_use "$v"
check "nothing is left owned by someone else" \
  test -z "$(as_root "$v" /data/attachments "find /data/attachments ! -user $uid -o ! -group $gid")"

echo "made by a person, not root: another uid, mode 700"
newvol person
as_root "$v" /data/attachments '
  mkdir -p /data/attachments/sub
  echo y > /data/attachments/sub/f; chmod 600 /data/attachments/sub/f
  chown -R 1000:1000 /data/attachments; chmod 700 /data/attachments /data/attachments/sub'
run_init "$v"
check "the app can use it" app_can_use "$v"

echo "a symlink in the directory is not followed"
newvol link
as_root "$v" /data '
  mkdir -p /data/outside /data/attachments
  echo secret > /data/outside/f
  ln -s ../outside /data/attachments/out
  ln -s ../outside/f /data/attachments/outfile
  chown 0:0 /data/attachments /data/outside /data/outside/f'
run_init "$v" /data
check "what the link points at is untouched" \
  test "$(as_root "$v" /data 'stat -c %u:%g /data/outside /data/outside/f' | tr '\n' ' ')" = "0:0 0:0 "

echo "already owned $owner: left alone"
newvol owned
as_root "$v" /data/attachments "
  mkdir -p /data/attachments/ab
  echo a > /data/attachments/ab/mine; echo b > /data/attachments/root-file
  chown -R $owner /data/attachments
  chown 0:0 /data/attachments/root-file      # chown -R would change this
  touch -d '2020-01-01 00:00:00' /data/attachments/ab/mine /data/attachments/root-file"
snap() { as_root "$v" /data/attachments 'find /data/attachments -exec stat -c "%n %u:%g %a %Y %Z" {} + | sort'; }
before="$(snap)"
sleep 2   # ctime has one-second resolution; a chown would move it
run_init "$v"
after="$(snap)"
check "no file's owner, mode, mtime or ctime changed" test "$before" = "$after"
check "the root-owned file inside was not re-owned (a recursive chown did not run)" \
  test "$(as_root "$v" /data/attachments 'stat -c %u /data/attachments/root-file')" = 0

echo "running it twice"
newvol twice; root_empty "$v"
run_init "$v"; run_init "$v"
check "still usable" app_can_use "$v"

echo
if [ "$fail" -ne 0 ]; then echo FAIL; exit 1; fi
echo PASS
