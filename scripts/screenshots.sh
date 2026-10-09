#!/usr/bin/env bash
#
# Regenerates the website screenshots from a freshly seeded instance.
#
#   ./scripts/screenshots.sh                    build, seed and capture into $SHOTS_OUT/png
#   ./scripts/screenshots.sh --publish DIR      also copy the PNGs into DIR/screenshots/
#                                               (DIR is a checkout of the web branch)
#   ./scripts/screenshots.sh --only 05,12       capture only these shots (seeding is always full)
#   ./scripts/screenshots.sh --keep             leave the database behind afterwards
#   ./scripts/screenshots.sh --help
#
# What it does: builds the frontend into the Go binary, starts that binary
# against a database it drops and recreates, with a stand-in for ClamAV
# (frontend/scripts/screenshots/fake-clamd.mjs) and an Inter font mapped to
# system-ui through FONTCONFIG_FILE. It then runs capture.mjs, seed.mjs and
# capture.mjs again, and writes report.txt. Nothing is written into the
# repository. The only tracked file it touches is
# backend/internal/ui/dist/index.html, which is restored on exit.
#
# Environment (default in brackets):
#   SHOTS_OUT             output directory [${TMPDIR:-/tmp}/ghd-shots]
#   SHOTS_PORT            HTTP port for the server [18080]
#   FAKE_CLAMD_PORT       scanner stand-in port [13310]
#   SHOTS_DB              database name; must start with ghd_screenshots [ghd_screenshots]
#   SHOTS_PG_ADMIN_URL    URL that may CREATE and DROP databases
#                         [postgres://helpdesk:helpdesk@localhost:5432/postgres?sslmode=disable]
#   SHOTS_CHROMIUM        browser executable [the pinned sandbox build if present, else Playwright's]
#   SHOTS_VERSION         footer version override, for previews only [unset: version.go]
#   SHOTS_PASSWORD        password for every seeded account [Screenshots-2026!]
#   SHOTS_REPUTATION      0 skips the CIRCL and VirusTotal shots' lookups [1]
#   SHOTS_WEBHOOK_OK_URL  optional public 2xx endpoint for a "Delivered" webhook state
#   SHOTS_KEEP            1 leaves the database in place (same as --keep)
#
# Sandbox notes: Inter 4.1 is downloaded once into
# ${XDG_CACHE_HOME:-$HOME/.cache}/ghd-shots/inter-4.1 and checked against its
# SHA-256. If the download fails, Liberation Sans is used and the run warns that
# the images will differ. Never run `playwright install` here.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fe="$root/frontend"
shots="$fe/scripts/screenshots"
ui_dist="$root/backend/internal/ui/dist"

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

out="${SHOTS_OUT:-${TMPDIR:-/tmp}/ghd-shots}"
port="${SHOTS_PORT:-18080}"
clamd_port="${FAKE_CLAMD_PORT:-13310}"
db="${SHOTS_DB:-ghd_screenshots}"
admin_url="${SHOTS_PG_ADMIN_URL:-postgres://helpdesk:helpdesk@localhost:5432/postgres?sslmode=disable}"
password="${SHOTS_PASSWORD:-Screenshots-2026!}"
chromium="${SHOTS_CHROMIUM:-}"
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

# Per-run secrets. They live only in this script's environment and the server's,
# never in a file, and they are long enough and free of the insecure-config
# markers, so the red banner stays off.
run_secret() { python3 -I -c 'import secrets; print(secrets.token_hex(32))'; }

mkdir -p "$out/png"
work=""
server_pid=""
clamd_pid=""
index_touched=0
db_created=0

cleanup() {
  set +e
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null
    wait "$server_pid" 2>/dev/null
  fi
  if [ -n "$clamd_pid" ]; then
    kill "$clamd_pid" 2>/dev/null
    wait "$clamd_pid" 2>/dev/null
  fi
  if [ "$index_touched" = 1 ]; then
    git -C "$root" checkout -- backend/internal/ui/dist/index.html 2>/dev/null \
      || warn "could not restore backend/internal/ui/dist/index.html: run git checkout -- it by hand"
  fi
  # The attachments directory holds the quarantined EICAR archive, so it goes
  # even when --keep is given.
  if [ -n "$work" ]; then rm -rf "$work"; fi
  if [ "$db_created" = 1 ]; then
    if [ "$keep" = 1 ]; then
      printf 'keeping database %s (--keep)\n' "$db"
    else
      psql -X -q "$admin_url" -c "DROP DATABASE IF EXISTS \"$db\" WITH (FORCE)" >/dev/null 2>&1 \
        || warn "could not drop database $db"
    fi
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# ── Preflight ─────────────────────────────────────────────────────────────────

preflight() {
  local t f
  for t in node npm go psql curl python3 sha256sum; do
    command -v "$t" >/dev/null 2>&1 || die "$t is not on PATH"
  done
  for f in fake-clamd.mjs seed.mjs capture.mjs; do
    [ -f "$shots/$f" ] || die "missing $shots/$f"
  done
  # Playwright keeps Chromium's profile under TMPDIR, and Chromium's socket path
  # in it must fit in a Unix socket address. Measured here: 61 characters
  # launches, 62 dies with "Target page, context or browser has been closed".
  local tmp="${TMPDIR:-/tmp}"
  if [ "${#tmp}" -gt 61 ]; then
    die "TMPDIR is ${#tmp} characters; Chromium cannot open its profile socket with more than 61. Point TMPDIR at a shorter directory."
  fi
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

# ── Build ─────────────────────────────────────────────────────────────────────

build() {
  if [ ! -d "$fe/node_modules" ]; then
    step "installing frontend dependencies (npm ci)"
    (cd "$fe" && npm ci)
  fi
  step "building the frontend"
  (cd "$fe" && npm run build)

  step "embedding the frontend in the server"
  # Only the tracked placeholder index.html stays; everything else under dist/
  # is ignored by git and is replaced.
  find "$ui_dist" -mindepth 1 -maxdepth 1 ! -name index.html -exec rm -rf {} +
  index_touched=1
  cp -r "$fe/dist/." "$ui_dist/"

  local ldflags=()
  if [ -n "${SHOTS_VERSION:-}" ]; then
    ldflags=(-ldflags "-X github.com/publiciallc/go-help-desk/backend/internal/version.Version=$SHOTS_VERSION")
  fi
  step "building the server"
  (cd "$root/backend" && go build -o "$work/ghd-server" ${ldflags[@]+"${ldflags[@]}"} ./cmd/server)

  local shown
  shown="${SHOTS_VERSION:-$(sed -n 's/^var Version = "\(.*\)"$/\1/p' "$root/backend/internal/version/version.go")}"
  printf 'the footer will read v%s\n' "$shown"
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
  curl -fsSL --retry 2 -o "$zip" "$inter_url" || return 1
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
  local family=Inter
  if ! have_inter; then
    step "fetching Inter 4.1"
    if ! fetch_inter; then
      warn "could not fetch Inter 4.1; using Liberation Sans, so the images will differ from the canonical set"
      family="Liberation Sans"
    fi
  fi
  write_fonts_conf "$family"
  export FONTCONFIG_FILE="$fonts_conf"
  if command -v fc-match >/dev/null 2>&1; then
    printf 'system-ui resolves to: %s\n' "$(fc-match system-ui)"
  fi
}

# ── Database ──────────────────────────────────────────────────────────────────

pg_ready() {
  psql -X -Atq "$admin_url" -c 'select 1' >/dev/null 2>&1
}

setup_db() {
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
  psql -X -q -v ON_ERROR_STOP=1 "$admin_url" \
    -c "DROP DATABASE IF EXISTS \"$db\" WITH (FORCE)" \
    -c "CREATE DATABASE \"$db\"" >/dev/null
  db_created=1

  db_url="$(python3 -I -c 'import sys, urllib.parse as u
p = u.urlsplit(sys.argv[1])
print(u.urlunsplit(p._replace(path="/" + sys.argv[2])))' "$admin_url" "$db")"
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
  # it directly. The variables the run must not inherit are cleared first.
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

capture() {
  local phase="$1"
  local args=(--phase "$phase")
  if [ -n "$only" ]; then args+=(--only "$only"); fi
  step "capture ($phase)"
  (cd "$fe" && node "$shots/capture.mjs" "${args[@]}") 2>&1 | tee -a "$out/capture.log"
}

seed() {
  step "seeding"
  (cd "$fe" && node "$shots/seed.mjs") 2>&1 | tee "$out/seed.log"
  [ -s "$out/seed.json" ] || die "seed.mjs did not write $out/seed.json"
}

optimise() {
  if command -v oxipng >/dev/null 2>&1; then
    step "optimising PNGs with oxipng"
    oxipng -o 4 --strip safe "$out"/png/*.png
  fi
}

report() {
  python3 -I - "$out/png" "$out/capture.log" "$out/report.txt" <<'PY'
import glob, os, re, struct, sys
png_dir, cap_log, report = sys.argv[1], sys.argv[2], sys.argv[3]
# Whole words only, and not inside a path such as wp2-cache-fail/.
note_re = re.compile(r"(?<![\w/-])(skip|skipped|fail|failed)(?![\w/-])", re.IGNORECASE)
LIMIT = 1_500_000
lines = []
total = 0
for p in sorted(glob.glob(os.path.join(png_dir, "*.png"))):
    size = os.path.getsize(p)
    total += size
    with open(p, "rb") as f:
        head = f.read(24)
    name = os.path.basename(p)
    if head[:8] != b"\x89PNG\r\n\x1a\n":
        lines.append(f"{name}  NOT A PNG")
        continue
    w, h = struct.unpack(">II", head[16:24])
    flag = "  OVER 1.5 MB" if size > LIMIT else ""
    lines.append(f"{name}  {w}x{h}  {size} bytes{flag}")
lines.append(f"total: {len(lines)} files, {total} bytes")
if os.path.exists(cap_log):
    with open(cap_log, encoding="utf-8", errors="replace") as f:
        notes = [l.rstrip() for l in f if note_re.search(l)]
    if notes:
        lines.append("")
        lines.append("skipped or failed:")
        lines.extend("  " + n for n in notes)
text = "\n".join(lines) + "\n"
with open(report, "w", encoding="utf-8") as f:
    f.write(text)
sys.stdout.write(text)
PY
}

publish_pngs() {
  step "copying PNGs into $publish/screenshots"
  cp "$out"/png/*.png "$publish/screenshots/"
}

# ── Main ──────────────────────────────────────────────────────────────────────

main() {
  preflight
  work="$(mktemp -d "${TMPDIR:-/tmp}/ghd-shots-work.XXXXXX")"
  # Each run starts from a clean record, so seed.json, the capture log and the
  # report describe this run only. PNGs are kept when --only is given.
  rm -f "$out/seed.json" "$out/capture.log" "$out/seed.log" "$out/report.txt"
  if [ -z "$only" ]; then rm -f "$out/png"/*.png; fi

  session_secret="$(run_secret)"
  jwt_secret="$(run_secret)"

  build
  setup_font
  setup_db
  start_clamd
  start_server
  export_for_node

  capture pre-setup
  seed
  capture main
  optimise
  report
  if [ -n "$publish" ]; then publish_pngs; fi
  step "done: $out"
}

main
