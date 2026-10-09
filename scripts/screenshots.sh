#!/usr/bin/env bash
#
# Regenerates the website screenshots from a freshly seeded 1.3.0 instance.
#
#   ./scripts/screenshots.sh                    build, seed and capture into a new output directory
#   ./scripts/screenshots.sh --publish DIR      also copy the shots this run wrote into DIR/screenshots/
#                                               (DIR is a checkout of the web branch)
#   ./scripts/screenshots.sh --only 05,12       capture only these shots (seeding is always full)
#   ./scripts/screenshots.sh --keep             leave the database behind afterwards
#   ./scripts/screenshots.sh --help
#
# Linux only. Chromium takes its fonts from fontconfig here, and that is how
# Inter is mapped to system-ui. On macOS the mapping does not apply, so the
# images would not match the set.
#
# What a run writes:
#   $SHOTS_OUT: default is a new directory under TMPDIR, printed at the start and
#     kept, so it can be published later. It holds png/, manifest.txt (the shots
#     this run wrote), seed.json (mode 0600: it holds the seeded passwords),
#     report.txt and the logs.
#   ${XDG_CACHE_HOME:-~/.cache}/ghd-shots/inter-4.1/: the Inter TTFs and
#     fonts.conf, kept between runs.
#   frontend/node_modules, only if missing. frontend/dist and
#     backend/internal/ui/dist/* are gitignored and replaced by the build; they
#     are left in place afterwards. The one tracked file there,
#     backend/internal/ui/dist/index.html, is restored on exit. The script
#     refuses to start while that file has uncommitted changes.
#   The PostgreSQL database named by SHOTS_DB, dropped on exit unless --keep.
#   A work directory under TMPDIR, named g.* and at most 61 characters long,
#   holding the server binary, the attachments (including the quarantined test
#   archive) and Chromium's profile. Removed on exit, even after a signal.
#
# Exposure: for the length of the run the server listens on every interface
# (HTTP_PORT binds ":port"). Each run uses a random SHOTS_PASSWORD unless one is
# set, and every seeded account has that password. Run this on a machine you
# trust, not on a shared host.
#
# Environment (default in brackets):
#   SHOTS_OUT                 output directory [a new mktemp directory under TMPDIR]
#   SHOTS_PORT                HTTP port for the server [18080]
#   FAKE_CLAMD_PORT           scanner stand-in port [13310]
#   SHOTS_DB                  database name; must start with ghd_screenshots [ghd_screenshots]
#   SHOTS_PG_ADMIN_URL        URL that may CREATE and DROP databases; its password is
#                             passed through PG* variables, not on a command line
#                             [postgres://helpdesk:helpdesk@localhost:5432/postgres?sslmode=disable]
#   SHOTS_CHROMIUM            browser executable [the pinned sandbox build if present, else Playwright's]
#   SHOTS_VERSION             footer version override, for previews only [unset: version.go]
#   SHOTS_PASSWORD            password for every seeded account [random per run]
#   SHOTS_REPUTATION          0 skips the CIRCL and VirusTotal lookups [1]
#   SHOTS_WEBHOOK_OK_URL      optional public 2xx endpoint for a "Delivered" webhook state
#   SHOTS_KEEP                1 leaves the database in place (same as --keep)
#   SHOTS_ALLOW_FALLBACK_FONT 1 lets --publish run when Inter could not be
#                             fetched and Liberation Sans is used instead. For
#                             previews only; the images will not match the set.
#
# Inter 4.1 is downloaded once and checked against its SHA-256. If the download
# fails, Liberation Sans is used and the run says so. Never run
# `playwright install` here; the pinned Chromium is used.
#
# Two parts of the capture contract are needed from frontend/scripts/screenshots/capture.mjs:
#   --list               print the shot ids that --only accepts, one per line, and exit 0
#                        before it launches a browser or reads seed.json
#   manifest.txt         each run writes $SHOTS_OUT/manifest.txt, one PNG file name per
#                        line, exactly the files that run wrote (empty if none)

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fe="$root/frontend"
shots="$fe/scripts/screenshots"
ui_dist="$root/backend/internal/ui/dist"
# The caller's TMPDIR. The run's own temporary files go under a work directory
# made from it, and TMPDIR is pointed there for the children (see main).
user_tmp="${TMPDIR:-/tmp}"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
warn() { printf 'warning: %s\n' "$*" >&2; }
step() { printf '==> %s\n' "$*"; }

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  awk 'NR <= 2 { next } /^#/ { sub(/^# ?/, ""); print; next } { exit }' "$0"
  exit 0
fi

# ── Arguments and configuration ───────────────────────────────────────────────

publish=""
keep="${SHOTS_KEEP:-0}"
only=""
while [ $# -gt 0 ]; do
  case "$1" in
    --publish) [ $# -ge 2 ] || die "--publish needs a directory"; publish="$2"; shift 2 ;;
    --publish=*) publish="${1#--publish=}"; shift ;;
    --keep) keep=1; shift ;;
    --only) [ $# -ge 2 ] || die "--only needs a list such as 05,12"; only="$2"; shift 2 ;;
    --only=*) only="${1#--only=}"; shift ;;
    *) die "unknown argument: $1 (see --help)" ;;
  esac
done

if [ -n "$only" ] && ! [[ "$only" =~ ^[0-9A-Za-z]+(,[0-9A-Za-z]+)*$ ]]; then
  die "--only takes a comma-separated list of shot ids, such as 05,12: got '$only'"
fi

if [ -n "$publish" ]; then
  [ -d "$publish/screenshots" ] || die "--publish: $publish/screenshots is not a directory (point it at the web branch checkout)"
  publish="$(cd "$publish" && pwd)"
fi

out_given="${SHOTS_OUT:-}"
port="${SHOTS_PORT:-18080}"
clamd_port="${FAKE_CLAMD_PORT:-13310}"
db="${SHOTS_DB:-ghd_screenshots}"
admin_url="${SHOTS_PG_ADMIN_URL:-postgres://helpdesk:helpdesk@localhost:5432/postgres?sslmode=disable}"
password="${SHOTS_PASSWORD:-}"
chromium="${SHOTS_CHROMIUM:-}"
allow_fallback_font="${SHOTS_ALLOW_FALLBACK_FONT:-0}"
pinned_chromium=/opt/pw-browsers/chromium-1194/chrome-linux/chrome

[[ "$port" =~ ^[0-9]+$ ]] || die "SHOTS_PORT must be a port number: got '$port'"
[[ "$clamd_port" =~ ^[0-9]+$ ]] || die "FAKE_CLAMD_PORT must be a port number: got '$clamd_port'"
# Refused by name, not by checking the server: a typo must never reach the
# development database, which the seed would otherwise populate.
[[ "$db" =~ ^ghd_screenshots[A-Za-z0-9_]*$ ]] \
  || die "SHOTS_DB must start with ghd_screenshots and hold only letters, digits and underscores: got '$db'"
if [ -n "${SHOTS_VERSION:-}" ] && ! [[ "$SHOTS_VERSION" =~ ^[0-9A-Za-z._+-]+$ ]]; then
  die "SHOTS_VERSION has characters the linker flag cannot take: '$SHOTS_VERSION'"
fi
if [ -z "$chromium" ] && [ -x "$pinned_chromium" ]; then
  chromium="$pinned_chromium"
fi
if [ -n "$chromium" ] && [ ! -x "$chromium" ]; then
  die "SHOTS_CHROMIUM is not executable: $chromium"
fi

# Per-run secrets. They live only in the environment of this script and its
# children, never in a file or on a command line, and they are long enough and
# free of the insecure-config markers, so the red banner stays off.
run_secret() { python3 -I -c 'import secrets; print(secrets.token_hex(24))'; }

# State shared with the trap. Everything the script creates is reachable from here.
out=""
work=""
server_pid=""
clamd_pid=""
step_pid=""
index_touched=0
db_created=0
pg_env=""
db_url=""
font_family=""
font_line=""
footer_version=""
footer_source=""
ldflags=()
session_secret=""
jwt_secret=""

# kill_tree stops a process and everything under it. Children are killed first,
# so nothing is reparented before it is found. Chromium is a grandchild of node.
kill_tree() {
  local child
  for child in $(pgrep -P "$1" 2>/dev/null || true); do
    kill_tree "$child"
  done
  kill "$1" 2>/dev/null || true
}

cleanup() {
  set +e
  if [ -n "$step_pid" ]; then kill_tree "$step_pid"; fi
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null
    wait "$server_pid" 2>/dev/null
  fi
  if [ -n "$clamd_pid" ]; then
    kill_tree "$clamd_pid"
    wait "$clamd_pid" 2>/dev/null
  fi
  if [ "$index_touched" = 1 ]; then
    git -C "$root" checkout -- backend/internal/ui/dist/index.html 2>/dev/null \
      || warn "could not restore backend/internal/ui/dist/index.html: run git checkout -- it by hand"
  fi
  # The attachments directory holds the quarantined EICAR archive, so it goes
  # even with --keep.
  if [ -n "$work" ]; then rm -rf "$work"; fi
  if [ "$db_created" = 1 ]; then
    if [ "$keep" = 1 ]; then
      printf 'keeping database %s (--keep)\n' "$db"
    else
      pgadmin -q -c "DROP DATABASE IF EXISTS \"$db\" WITH (FORCE)" >/dev/null 2>&1 \
        || warn "could not drop database $db"
    fi
  fi
}
trap cleanup EXIT
# A signal stops the child that is running now, instead of waiting for it to
# finish. Bash runs the trap as soon as `wait` returns.
trap 'exit 130' INT
trap 'exit 143' TERM

# run_logged LOG CMD [ARGS...] runs a command, or a function, as a background
# child, copies its output to LOG and to the terminal, and waits for it. Because
# the wait is on a child, a signal reaches the trap at once, and the trap kills
# the child's tree.
run_logged() {
  local log="$1"
  shift
  "$@" > >(tee -a "$log") 2>&1 &
  step_pid=$!
  local rc=0
  wait "$step_pid" || rc=$?
  step_pid=""
  return "$rc"
}

# ── Preflight ─────────────────────────────────────────────────────────────────

preflight() {
  local t f
  if [ "$(uname -s)" != Linux ]; then
    die "this runs on Linux only: Chromium takes its fonts from fontconfig here, and on macOS the Inter mapping does not apply, so the images would not match"
  fi
  for t in node npm go psql curl python3 sha256sum git pgrep; do
    command -v "$t" >/dev/null 2>&1 || die "$t is not on PATH"
  done
  git -C "$root" rev-parse --is-inside-work-tree >/dev/null 2>&1 || die "$root is not a git checkout"
  if [ -n "$(git -C "$root" status --porcelain -- backend/internal/ui/dist/index.html)" ]; then
    die "backend/internal/ui/dist/index.html has uncommitted changes. The run restores that file with git checkout, which would discard them. Commit or stash them first."
  fi
  for f in fake-clamd.mjs seed.mjs capture.mjs; do
    [ -f "$shots/$f" ] || die "missing $shots/$f"
  done
  if [ -z "$chromium" ]; then
    printf 'note: no SHOTS_CHROMIUM and no pinned build; capture uses Playwright'\''s own browser\n'
  fi
}

port_open() {
  python3 -I -c 'import socket,sys; socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=1).close()' "$1" 2>/dev/null
}

# A listener that is already there would answer for the run and hide a failure
# to start, so refuse to start over it.
require_free_port() {
  if port_open "$1"; then
    die "port $1 already has a listener; stop it or pick another ($2)"
  fi
}

npm_ci() { cd "$fe" && npm ci; }

ensure_deps() {
  if [ ! -d "$fe/node_modules" ]; then
    step "installing frontend dependencies (npm ci)"
    run_logged "$work/npm-ci.log" npm_ci
  fi
}

# --only ids come from the capture script itself, so the list cannot drift from
# the shot table.
capture_ids() {
  ( cd "$fe" && node "$shots/capture.mjs" --list )
}

validate_only() {
  local valid bad=() id
  local -a ids
  valid="$(capture_ids)" || die "capture.mjs --list failed; it must print the shot ids, one per line"
  IFS=, read -ra ids <<< "$only"
  for id in "${ids[@]}"; do
    grep -qxF -- "$id" <<< "$valid" || bad+=("$id")
  done
  if [ "${#bad[@]}" -gt 0 ]; then
    die "--only names no shot: ${bad[*]}. Valid ids: $(tr '\n' ' ' <<< "$valid")"
  fi
}

# ── Build ─────────────────────────────────────────────────────────────────────

build_frontend() { cd "$fe" && npm run build; }

build_server() {
  cd "$root/backend" && go build -o "$work/ghd-server" ${ldflags[@]+"${ldflags[@]}"} ./cmd/server
}

build() {
  step "building the frontend"
  run_logged "$out/build.log" build_frontend

  step "embedding the frontend in the server"
  # Only the tracked placeholder index.html stays. Everything else under dist/
  # is gitignored and replaced.
  find "$ui_dist" -mindepth 1 -maxdepth 1 ! -name index.html -exec rm -rf {} +
  index_touched=1
  cp -r "$fe/dist/." "$ui_dist/"

  if [ -n "${SHOTS_VERSION:-}" ]; then
    ldflags=(-ldflags "-X github.com/publiciallc/go-help-desk/backend/internal/version.Version=$SHOTS_VERSION")
    footer_version="$SHOTS_VERSION"
    footer_source="SHOTS_VERSION override"
  else
    footer_version="$(sed -n 's/^var Version = "\(.*\)"$/\1/p' "$root/backend/internal/version/version.go")"
    footer_source="backend/internal/version/version.go"
  fi
  step "building the server (footer will read v$footer_version)"
  run_logged "$out/build.log" build_server
}

# ── Font ──────────────────────────────────────────────────────────────────────

inter_url=https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip
inter_sha256=9883fdd4a49d4fb66bd8177ba6625ef9a64aa45899767dde3d36aa425756b11e
font_dir="${XDG_CACHE_HOME:-$HOME/.cache}/ghd-shots/inter-4.1"
fonts_conf="$font_dir/fonts.conf"
inter_files=(Inter-Regular.ttf Inter-Italic.ttf Inter-Medium.ttf Inter-SemiBold.ttf Inter-Bold.ttf)

have_inter() {
  local f
  for f in "${inter_files[@]}"; do
    [ -s "$font_dir/ttf/$f" ] || return 1
  done
}

fetch_inter() {
  local zip="$work/Inter-4.1.zip"
  curl -fsSL --retry 2 --max-time 300 -o "$zip" "$inter_url" || return 1
  printf '%s  %s\n' "$inter_sha256" "$zip" | sha256sum -c --status || {
    warn "Inter-4.1.zip does not match its SHA-256"
    return 1
  }
  mkdir -p "$font_dir/ttf"
  # errexit is off inside `if !`, so each failure returns explicitly.
  python3 -I - "$zip" "$font_dir/ttf" "${inter_files[@]}" <<'PY' || return 1
import os, sys, zipfile
zpath, dest, names = sys.argv[1], sys.argv[2], sys.argv[3:]
with zipfile.ZipFile(zpath) as z:
    for n in names:
        # The names are fixed above, so nothing from the archive chooses a path.
        with open(os.path.join(dest, n), "wb") as f:
            f.write(z.read("extras/ttf/" + n))
PY
}

# Chromium reads its fonts through fontconfig, and a woff2 that fontconfig
# matches is ignored, so the TTFs are used. The rules map the generic names the
# app asks for (index.css: system-ui) to the family. Plain <alias> did not work.
write_fonts_conf() {
  local family="$1"
  mkdir -p "$font_dir/fc-cache"
  {
    printf '%s\n' '<?xml version="1.0"?>' \
      '<!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">' \
      '<fontconfig>' \
      '  <include ignore_missing="yes">/etc/fonts/fonts.conf</include>'
    if [ "$family" = Inter ]; then
      printf '  <dir>%s</dir>\n' "$font_dir/ttf"
    fi
    printf '  <cachedir>%s</cachedir>\n' "$font_dir/fc-cache"
    printf '%s\n' \
      '  <match target="pattern">' \
      '    <test qual="any" name="family"><string>system-ui</string></test>' \
      "    <edit name=\"family\" mode=\"assign\" binding=\"strong\"><string>$family</string></edit>" \
      '  </match>'
    local g
    for g in sans-serif sans; do
      printf '%s\n' \
        '  <match target="pattern">' \
        "    <test qual=\"any\" name=\"family\"><string>$g</string></test>" \
        "    <edit name=\"family\" mode=\"prepend\" binding=\"strong\"><string>$family</string></edit>" \
        '  </match>'
    done
    printf '%s\n' '</fontconfig>'
  } > "$fonts_conf"
}

setup_font() {
  font_family=Inter
  if ! have_inter; then
    step "fetching Inter 4.1"
    if ! fetch_inter; then
      warn "could not fetch Inter 4.1; using Liberation Sans. The images will differ from the canonical set"
      font_family="Liberation Sans"
    fi
  fi
  write_fonts_conf "$font_family"
  export FONTCONFIG_FILE="$fonts_conf"
  font_line="$font_family"
  if command -v fc-match >/dev/null 2>&1; then
    font_line="$font_family (fc-match system-ui: $(fc-match system-ui))"
    printf 'system-ui resolves to: %s\n' "$(fc-match system-ui)"
  fi
}

# ── Database ──────────────────────────────────────────────────────────────────

# The admin URL is handed to Python in the environment, not in argv, so its
# password does not show in `ps`. The PG* values it prints are applied only
# inside pgadmin(), never to the rest of the run.
pg_env_py='import os, shlex, urllib.parse as u
p = u.urlsplit(os.environ["PG_ADMIN_URL"])
q = dict(u.parse_qsl(p.query))
vals = {"PGHOST": p.hostname or "localhost", "PGPORT": str(p.port or 5432),
        "PGUSER": u.unquote(p.username or ""), "PGPASSWORD": u.unquote(p.password or ""),
        "PGDATABASE": u.unquote(p.path.lstrip("/")) or "postgres"}
if q.get("sslmode"):
    vals["PGSSLMODE"] = q["sslmode"]
for k, v in vals.items():
    if v:
        print("export %s=%s" % (k, shlex.quote(v)))'

db_url_py='import os, urllib.parse as u
p = u.urlsplit(os.environ["PG_ADMIN_URL"])
print(u.urlunsplit(p._replace(path="/" + os.environ["SHOTS_DB_NAME"])))'

pgadmin() {
  ( eval "$pg_env"; exec psql -X -v ON_ERROR_STOP=1 "$@" )
}

pg_ready() {
  pgadmin -Atq -c 'select 1' >/dev/null 2>&1
}

setup_db() {
  pg_env="$(PG_ADMIN_URL="$admin_url" python3 -I -c "$pg_env_py")"
  if ! pg_ready; then
    if command -v pg_ctlcluster >/dev/null 2>&1; then
      step "starting the local Postgres 16 cluster"
      pg_ctlcluster 16 main start >/dev/null 2>&1 || true
    fi
    local i
    for i in $(seq 1 30); do
      pg_ready && break
      sleep 1
    done
  fi
  pg_ready || die "cannot reach Postgres through SHOTS_PG_ADMIN_URL"

  step "recreating database $db"
  pgadmin -q -c "DROP DATABASE IF EXISTS \"$db\" WITH (FORCE)" -c "CREATE DATABASE \"$db\"" >/dev/null
  db_created=1

  db_url="$(PG_ADMIN_URL="$admin_url" SHOTS_DB_NAME="$db" python3 -I -c "$db_url_py")"
}

# ── Services ──────────────────────────────────────────────────────────────────

clamd_pong() {
  python3 -I -c 'import socket,sys
try:
    s = socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=2)
    s.sendall(b"zPING\0")
    ok = s.recv(16).rstrip(b"\0\n") == b"PONG"
except OSError:
    ok = False
sys.exit(0 if ok else 1)' "$clamd_port" 2>/dev/null
}

start_clamd() {
  require_free_port "$clamd_port" "FAKE_CLAMD_PORT"
  step "starting the scanner stand-in on 127.0.0.1:$clamd_port"
  FAKE_CLAMD_PORT="$clamd_port" node "$shots/fake-clamd.mjs" > "$out/clamd.log" 2>&1 &
  clamd_pid=$!
  local i
  for i in $(seq 1 20); do
    if clamd_pong; then return 0; fi
    kill -0 "$clamd_pid" 2>/dev/null || break
    sleep 0.5
  done
  cat "$out/clamd.log" >&2 || true
  die "scanner stand-in did not answer PONG"
}

start_server() {
  require_free_port "$port" "SHOTS_PORT"
  mkdir -p "$work/attachments"
  step "starting the server on http://localhost:$port"
  # The subshell execs the server, so $! is the server itself and `kill` reaches
  # it directly. Variables the run must not inherit are cleared first.
  (
    for v in $(compgen -e); do
      case "$v" in
        SMTP_*|APP_ENV|SLA_ENABLED) unset "$v" ;;
      esac
    done
    export DATABASE_URL="$db_url"
    export BASE_URL="http://localhost:$port"
    export HTTP_PORT="$port"
    export SESSION_SECRET="$session_secret"
    export JWT_SECRET="$jwt_secret"
    export ATTACHMENT_DIR="$work/attachments"
    export CLAMAV_ADDR="tcp://127.0.0.1:$clamd_port"
    export AUTH_RATE_LIMIT_PER_MINUTE=0
    export AUTH_THROTTLE_DELAY=0
    export LOG_LEVEL=info
    exec "$work/ghd-server"
  ) > "$out/server.log" 2>&1 &
  server_pid=$!

  local i
  for i in $(seq 1 60); do
    if curl -fsS -o /dev/null --max-time 2 "http://127.0.0.1:$port/health" 2>/dev/null; then
      return 0
    fi
    if ! kill -0 "$server_pid" 2>/dev/null; then
      tail -n 40 "$out/server.log" >&2 || true
      die "the server exited during start-up (log above, in $out/server.log)"
    fi
    sleep 1
  done
  tail -n 40 "$out/server.log" >&2 || true
  die "the server did not answer /health within 60s"
}

# ── Capture, seed and report ──────────────────────────────────────────────────

export_for_node() {
  export SHOTS_BASE_URL="http://localhost:$port"
  export SHOTS_OUT="$out"
  export SHOTS_PASSWORD="$password"
  export SHOTS_REPUTATION="${SHOTS_REPUTATION:-1}"
  export DATABASE_URL="$db_url"
  if [ -n "$chromium" ]; then export SHOTS_CHROMIUM="$chromium"; fi
  if [ -n "${SHOTS_WEBHOOK_OK_URL:-}" ]; then export SHOTS_WEBHOOK_OK_URL; fi
}

node_capture() { ( cd "$fe" && node "$shots/capture.mjs" "$@" ); }
# seed.json holds the seeded passwords, so the seed writes it under umask 077.
node_seed() { ( umask 077; cd "$fe" && node "$shots/seed.mjs" ); }

# capture PHASE: each phase writes its own manifest; the file is moved aside so
# the next phase cannot overwrite it.
capture() {
  local phase="$1"
  local args=(--phase "$phase")
  if [ -n "$only" ]; then args+=(--only "$only"); fi
  step "capture ($phase)"
  rm -f "$out/manifest.txt"
  run_logged "$out/capture.log" node_capture "${args[@]}"
  [ -f "$out/manifest.txt" ] || die "capture ($phase) wrote no manifest.txt"
  mv "$out/manifest.txt" "$work/manifest.$phase"
}

seed() {
  step "seeding"
  run_logged "$out/seed.log" node_seed
  [ -s "$out/seed.json" ] || die "seed.mjs did not write $out/seed.json"
  chmod 600 "$out/seed.json"
}

# The run's manifest is the union of the two phases. It is what --publish and
# the report read; png/ may hold shots from earlier runs that this one did not write.
merge_manifest() {
  cat "$work/manifest.pre-setup" "$work/manifest.main" | sort -u > "$out/manifest.txt"
}

optimise() {
  command -v oxipng >/dev/null 2>&1 || return 0
  step "optimising PNGs with oxipng"
  local f
  while IFS= read -r f; do
    oxipng -o 4 --strip safe "$out/png/$f"
  done < "$out/manifest.txt"
}

report() {
  {
    printf 'font: %s\n' "$font_line"
    printf 'footer: v%s (%s)\n' "$footer_version" "$footer_source"
    printf 'chromium: %s\n' "${chromium:-Playwright default}"
    printf 'output: %s\n' "$out"
    printf 'manifest: %s files\n' "$(wc -l < "$out/manifest.txt" | tr -d ' ')"
    printf '\n'
  } > "$out/report.txt"
  python3 -I - "$out/png" "$out/manifest.txt" "$out/capture.log" "$out/report.txt" <<'PY'
import os, re, struct, sys
png_dir, manifest, cap_log, report = sys.argv[1:5]
# Whole words only, and not inside a path such as wp2-cache-fail/.
note_re = re.compile(r"(?<![\w/-])(skip|skipped|fail|failed)(?![\w/-])", re.IGNORECASE)
LIMIT = 1_500_000
lines = []
total = 0
with open(manifest, encoding="utf-8") as f:
    names = [l.strip() for l in f if l.strip()]
for name in names:
    p = os.path.join(png_dir, name)
    if not os.path.exists(p):
        lines.append(f"{name}  MISSING")
        continue
    size = os.path.getsize(p)
    total += size
    with open(p, "rb") as f:
        head = f.read(24)
    if head[:8] != b"\x89PNG\r\n\x1a\n":
        lines.append(f"{name}  NOT A PNG")
        continue
    w, h = struct.unpack(">II", head[16:24])
    flag = "  OVER 1.5 MB" if size > LIMIT else ""
    lines.append(f"{name}  {w}x{h}  {size} bytes{flag}")
lines.append(f"total: {len(names)} files, {total} bytes")
if os.path.exists(cap_log):
    with open(cap_log, encoding="utf-8", errors="replace") as f:
        notes = [l.rstrip() for l in f if note_re.search(l)]
    if notes:
        lines.append("")
        lines.append("skipped or failed:")
        lines.extend("  " + n for n in notes)
text = "\n".join(lines) + "\n"
with open(report, "a", encoding="utf-8") as f:
    f.write(text)
sys.stdout.write(text)
PY
}

# --publish copies exactly the files the manifest lists. It refuses to copy
# anything if the manifest is missing or empty, or names a file that is not
# there, so a stale PNG from an earlier run cannot reach the web branch.
publish_pngs() {
  [ -s "$out/manifest.txt" ] || die "this run wrote no shots (manifest.txt is missing or empty); nothing to publish"
  local dest="$publish/screenshots" f
  while IFS= read -r f; do
    [[ "$f" =~ ^[0-9A-Za-z._-]+\.png$ ]] || die "manifest names an unexpected file: $f"
    [ -f "$out/png/$f" ] || die "manifest lists $f, but $out/png/$f is missing"
  done < "$out/manifest.txt"
  step "copying $(wc -l < "$out/manifest.txt" | tr -d ' ') PNGs listed in the manifest into $dest"
  while IFS= read -r f; do
    cp "$out/png/$f" "$dest/$f"
    chmod 644 "$dest/$f"
  done < "$out/manifest.txt"
}

# ── Main ──────────────────────────────────────────────────────────────────────

main() {
  preflight
  # Checks that can fail come before the output directory is created, so a
  # rejected run leaves nothing behind but its work directory, which is removed.
  work="$(mktemp -d "$user_tmp/g.XXXXXX")"
  # Chromium's profile is created under TMPDIR, so the children get the work
  # directory as TMPDIR: exit removes the profile even after a signal. Chromium's
  # socket path must fit in a Unix socket address. Measured: a TMPDIR of 61
  # characters launches, 62 dies with "Target page, context or browser has been closed".
  if [ "${#work}" -gt 61 ]; then
    die "the work directory path under TMPDIR is ${#work} characters; Chromium cannot open its profile socket with more than 61. Point TMPDIR at a shorter directory."
  fi
  export TMPDIR="$work"
  ensure_deps
  if [ -n "$only" ]; then validate_only; fi

  setup_font
  if [ -n "$publish" ] && [ "$font_family" != Inter ] && [ "$allow_fallback_font" != 1 ]; then
    die "Inter could not be fetched, so this run would use $font_family, and the images would not match the set. Refusing --publish. For a preview only, set SHOTS_ALLOW_FALLBACK_FONT=1."
  fi

  if [ -n "$out_given" ]; then
    out="$out_given"
    # Private if this creates it. Existing directories keep their mode.
    ( umask 077; mkdir -p "$out" )
  else
    out="$(mktemp -d "$user_tmp/ghd-shots.XXXXXX")"
  fi
  printf 'output directory: %s\n' "$out"
  mkdir -p "$out/png"

  # Each run starts from a clean record, so seed.json, the logs, the manifest
  # and the report describe this run only. PNGs are kept when --only is given.
  rm -f "$out/seed.json" "$out/capture.log" "$out/seed.log" "$out/report.txt" "$out/manifest.txt" "$out/build.log"
  if [ -z "$only" ]; then rm -f "$out/png"/*.png; fi

  if [ -z "$password" ]; then password="$(run_secret)"; fi
  session_secret="$(run_secret)"
  jwt_secret="$(run_secret)"

  build
  setup_db
  start_clamd
  start_server
  export_for_node

  capture pre-setup
  seed
  capture main
  merge_manifest
  optimise
  report
  if [ -n "$publish" ]; then publish_pngs; fi
  step "done: $out"
}

main
