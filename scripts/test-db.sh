#!/usr/bin/env bash
#
# Lifecycle for the ephemeral test database (docker/docker-compose.test.yml).
#
#   ./scripts/test-db.sh up           start it, wait until it accepts connections
#   ./scripts/test-db.sh test [args]  start it, run the suite, leave it running
#   ./scripts/test-db.sh once [args]  start it, run the suite, tear everything down
#   ./scripts/test-db.sh down         destroy it
#   ./scripts/test-db.sh url          print the DSN
#   ./scripts/test-db.sh psql         open a shell on it
#
# Several at once: set GHD_TEST_INSTANCE to a short name (letters, digits,
# dashes) and each name gets its own database on its own free port, e.g.
#   GHD_TEST_INSTANCE=pr368 ./scripts/test-db.sh once
# Without it there is one shared database, ghd-test on 5433, and two runs at
# once tear each other's database down: `once` ends with `down -v`, which
# removes it under whatever else is still using it. Worktrees and parallel
# agents should always name an instance. GHD_TEST_PORT pins the port.
#
# Nothing is left running. If the colima VM was not up when the script started
# it, `down` and `once` stop it again — so no VM idles on the machine between
# test runs. A colima you started yourself is left strictly alone.
#
# There is no seed step. testutil.NewDB runs the migrations itself on first
# connect, and every test wraps its work in a transaction that is rolled back,
# so the database needs no preparation and no cleanup between runs.
#
# Extra arguments are passed to `go test`, so a single package iterates fast:
#   ./scripts/test-db.sh test ./internal/server/ -run OIDC

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="$repo_root/docker/docker-compose.test.yml"

die() { printf '%s\n' "$*" >&2; exit 1; }

instance="${GHD_TEST_INSTANCE:-}"
if [ -n "$instance" ]; then
  [[ "$instance" =~ ^[a-z0-9][a-z0-9-]{0,40}$ ]] || die \
    "GHD_TEST_INSTANCE must be lowercase letters, digits and dashes: got '$instance'"
  project="ghd-test-$instance"
  # The port is remembered per instance, so `up`, `test`, `url` and `down`
  # run separately all find the same database.
  port_file="${TMPDIR:-/tmp}/ghd-test-db-$instance.port"
  if [ -z "${GHD_TEST_PORT:-}" ]; then
    if [ -f "$port_file" ]; then
      GHD_TEST_PORT="$(cat "$port_file")"
    else
      GHD_TEST_PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')" \
        || die "could not pick a free port; set GHD_TEST_PORT"
      printf '%s\n' "$GHD_TEST_PORT" > "$port_file"
    fi
  fi
else
  project="ghd-test"
  port_file=""
fi

# The database is published on 127.0.0.1:5433 by default — not 5432, which the
# dev stack may hold. Pointing the suite at the development database would
# migrate it.
export GHD_TEST_PORT="${GHD_TEST_PORT:-5433}"
[ "$GHD_TEST_PORT" != "5432" ] || die "GHD_TEST_PORT=5432 is the dev stack's port; pick another"
export TEST_DATABASE_URL="postgres://helpdesk:helpdesk@127.0.0.1:$GHD_TEST_PORT/helpdesk_test?sslmode=disable"

# Records that this script — not the developer — started the colima VM, so
# teardown knows whether stopping it would interrupt someone else's work.
colima_marker="${TMPDIR:-/tmp}/ghd-test-db-started-colima"

compose() {
  if docker compose version >/dev/null 2>&1; then
    docker compose -p "$project" -f "$compose_file" "$@"
  elif command -v docker-compose >/dev/null 2>&1; then
    docker-compose -p "$project" -f "$compose_file" "$@"
  else
    die "docker compose not found. Install a container runtime — see CONTRIBUTING.md."
  fi
}

require_runtime() {
  command -v docker >/dev/null 2>&1 || die \
    "docker not found.

A container runtime is needed to run the integration suite. On macOS:

    brew install colima docker docker-compose

Colima is Apache-2.0 and free for commercial use; Docker Desktop and OrbStack
both require a paid licence for business use above their size thresholds. Do
not use \`brew services start colima\` — that restarts the VM at every login,
which is the thing this setup exists to avoid.

The unit tests need none of this:
    cd backend && go test ./internal/domain/... ./internal/config/... ./internal/middleware/..."

  if docker info >/dev/null 2>&1; then
    return 0
  fi

  command -v colima >/dev/null 2>&1 || die \
    "docker is installed but no daemon is responding, and colima is not present.
Start your container runtime, or: brew install colima"

  printf 'starting colima (it was not running)\n'
  colima start >/dev/null 2>&1 || die "colima failed to start. Try: colima start"

  # Only now, having started it ourselves, may teardown stop it again.
  : > "$colima_marker"

  docker info >/dev/null 2>&1 || die "colima started but docker is still unreachable"
}

# stop_colima_if_ours stops the VM only when this script started it. A developer
# who had colima up for other work keeps it.
stop_colima_if_ours() {
  [ -f "$colima_marker" ] || return 0
  # Another instance still running needs the VM. Whichever run ends last
  # stops it.
  if docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^ghd-test'; then
    printf 'leaving colima up: another test database is still running\n'
    return 0
  fi
  rm -f "$colima_marker"
  if command -v colima >/dev/null 2>&1; then
    printf 'stopping colima (this script started it)\n'
    colima stop >/dev/null 2>&1 || printf 'could not stop colima; leaving it up\n' >&2
  fi
}

wait_ready() {
  printf 'waiting for postgres'
  for _ in $(seq 1 60); do
    if compose exec -T testdb pg_isready -U helpdesk -d helpdesk_test >/dev/null 2>&1; then
      printf ' ready\n'
      return 0
    fi
    printf '.'
    sleep 1
  done
  printf '\n'
  die "postgres did not become ready within 60s. Logs:
$(compose logs --tail 30 testdb 2>&1)"
}

cmd_up() {
  require_runtime
  compose up -d --wait 2>/dev/null || compose up -d
  wait_ready
  printf 'TEST_DATABASE_URL=%s\n' "$TEST_DATABASE_URL"
}

cmd_down() {
  # Deliberately not require_runtime: if there is no daemon there is nothing
  # to tear down, and starting a VM in order to stop it would be absurd.
  if docker info >/dev/null 2>&1; then
    # -v removes the volumes too. The data directory is tmpfs, so stopping the
    # container already discards it; -v covers anything added later.
    compose down -v --remove-orphans
  else
    printf 'no docker daemon; nothing to tear down\n'
  fi
  [ -z "$port_file" ] || rm -f "$port_file"
  stop_colima_if_ours
}

cmd_test() {
  cmd_up
  local args=("$@")
  [ ${#args[@]} -eq 0 ] && args=("./...")
  printf '\nrunning: go test %s\n\n' "${args[*]}"
  (cd "$repo_root/backend" && go test "${args[@]}")
}

case "${1:-}" in
  up)   shift; cmd_up ;;
  down) shift; cmd_down ;;
  test) shift; cmd_test "$@" ;;
  once)
    shift
    status=0
    # Tear down even if the suite fails or the run is interrupted, so a failed
    # test never leaves a VM and a database running on the machine.
    trap 'cmd_down' EXIT INT TERM
    cmd_test "$@" || status=$?
    printf '\ntearing down\n'
    exit $status
    ;;
  url)  printf '%s\n' "$TEST_DATABASE_URL" ;;
  psql)
    require_runtime
    compose exec testdb psql -U helpdesk -d helpdesk_test
    ;;
  *)
    sed -n '3,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    exit 1
    ;;
esac
