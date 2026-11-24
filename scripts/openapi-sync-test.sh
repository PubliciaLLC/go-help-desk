#!/usr/bin/env bash
# Exercises scripts/openapi-sync.sh against a throwaway local "app repository"
# (a bare origin with a release branch, a release tag, a hotfix tag and a feature
# branch), so nothing here touches the network or this repository's own history.
#
# The cases that matter:
#   * pull writes the commit it resolved, not the ref it was given, and labels the
#     note with the optional name;
#   * check refuses a pin that only a feature branch holds (the pin that dies
#     when that branch is deleted after a squash merge), and also one that only
#     a branch merely named like a release branch holds;
#   * check still accepts a pin on main, on a v*-beta branch or on a tag alone;
#   * every refusal leaves the working files as they were.
#
# Run it from anywhere: scripts/openapi-sync-test.sh
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
sync="$here/openapi-sync.sh"
fail=0

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Isolate git from the developer's own configuration (signing, hooks, defaults).
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.com
export GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.com

app="$tmp/app"
origin="$tmp/origin.git"
site="$tmp/site"

# --- the app repository -----------------------------------------------------

git init -q -b main "$app"
git init -q --bare -b main "$origin"
git -C "$app" remote add origin "$origin"

spec() { # spec <text>: commit docs/api/openapi.yaml holding <text>, print the sha
  mkdir -p "$app/docs/api"
  printf '%s\n' "$1" > "$app/docs/api/openapi.yaml"
  git -C "$app" add docs/api/openapi.yaml
  git -C "$app" commit -q -m "$1"
  git -C "$app" rev-parse HEAD
}

printf 'readme\n' > "$app/README"
git -C "$app" add README
git -C "$app" commit -q -m "no spec yet"
main_nospec=$(git -C "$app" rev-parse HEAD)
main_sha=$(spec "spec-main")

git -C "$app" switch -q -c v1.3.0-beta
b1=$(spec "spec-1")
git -C "$app" tag -a v1.3.0 -m v1.3.0
b2=$(spec "spec-2")
git -C "$app" tag -a v1.4.0-beta -m "a prerelease tag stale must ignore"

git -C "$app" switch -q -c feature/x
f1=$(spec "spec-feature")

git -C "$app" switch -q -c v1.3.0-beta-web "$b2"
w1=$(spec "spec-web")

git -C "$app" switch -q -c nospec "$main_nospec"

git -C "$app" switch -q -c hotfix "$main_sha"
h1=$(spec "spec-hotfix")
git -C "$app" tag -a v1.2.9 -m v1.2.9

git -C "$app" push -q origin main v1.3.0-beta feature/x v1.3.0-beta-web nospec
git -C "$app" push -q origin v1.3.0 v1.4.0-beta v1.2.9 # h1 is on a tag only

# --- the site clone ----------------------------------------------------------

git init -q -b site "$site"
git -C "$site" remote add origin "$origin"
mkdir -p "$site/docs/openapi"
cd "$site"
git fetch -q origin --tags # what the script's own header tells a local clone to do

# run <args...>: run the script in the site clone, capture $out and $rc.
out="" rc=0
run() {
  rc=0
  out="$("$sync" "$@" 2>&1)" || rc=$?
}

# first_line <file>
first_line() { sed -n 1p "$1"; }

pass() { printf '  ok   %s\n' "$1"; }
bad() {
  printf '  FAIL %s\n' "$1"
  [ -z "${2:-}" ] || printf '       %s\n' "$2"
  fail=1
}

# ok <desc> <args...>: the script succeeds.
ok() {
  local desc="$1"; shift
  run "$@"
  if [ "$rc" -eq 0 ]; then pass "$desc"; else bad "$desc" "exit $rc: $out"; fi
}

# refused <desc> <pattern> <args...>: the script fails and its output matches.
refused() {
  local desc="$1" pat="$2"; shift 2
  run "$@"
  if [ "$rc" -eq 0 ]; then
    bad "$desc" "was accepted: $out"
  elif ! printf '%s' "$out" | grep -Eq -- "$pat"; then
    bad "$desc" "refused, but not with /$pat/: $out"
  else
    pass "$desc"
  fi
}

# is <desc> <got> <want>
is() {
  if [ "$2" = "$3" ]; then pass "$1"; else bad "$1" "got [$2] want [$3]"; fi
}

# note_names <desc> <label>: SOURCE's note line says "taken from ref <label>."
note_names() {
  if grep -qF "taken from ref $2. See" docs/openapi/SOURCE; then
    pass "$1"
  else
    bad "$1" "$(cat docs/openapi/SOURCE)"
  fi
}

# pin <sha>: hand-write SOURCE and a matching copy, as a pin made elsewhere.
pin() {
  printf '%s\n# hand-written\n' "$1" > docs/openapi/SOURCE
  git -C "$app" show "$1:docs/api/openapi.yaml" > docs/openapi/openapi.yaml
}

echo "pull"
ok "pull a branch" pull v1.3.0-beta
is "  SOURCE names the commit, not the branch" "$(first_line docs/openapi/SOURCE)" "$b2"
is "  the copy is the spec at that commit" "$(cat docs/openapi/openapi.yaml)" "spec-2"
note_names "  the note names the ref" v1.3.0-beta
ok "check passes right after a pull" check

ok "pull an exact sha with a label" pull "$b1" v1.3.0
is "  SOURCE holds the sha" "$(first_line docs/openapi/SOURCE)" "$b1"
note_names "  the note carries the label, not the sha" v1.3.0
is "  the copy follows the sha" "$(cat docs/openapi/openapi.yaml)" "spec-1"
ok "check passes on a pin made by sha" check

ok "pull a tag" pull v1.3.0
note_names "  the note defaults to the ref" v1.3.0

echo "pull refusals leave the files alone"
before="$(cat docs/openapi/SOURCE docs/openapi/openapi.yaml)"
refused "no ref" 'usage: .* pull <ref>' pull
refused "a ref origin does not have" 'cannot fetch' pull no-such-branch
refused "a commit without the spec" 'does not exist at' pull nospec
refused "an unknown subcommand" 'usage:' frobnicate
refused "no subcommand" 'usage:'
is "  SOURCE and the copy are untouched" "$(cat docs/openapi/SOURCE docs/openapi/openapi.yaml)" "$before"

echo "check: what may be pinned"
pin "$b2"
ok "a commit on a v*-beta branch" check
pin "$main_sha"
ok "a commit on main" check
pin "$h1"
ok "a commit only a v* tag holds" check

pin "$f1"
refused "a commit only a feature branch holds" 'not on origin/main, an origin/v\*-beta branch or a v\* tag' check
pin "$w1"
refused "a commit only v1.3.0-beta-web holds (not a v*-beta branch)" 'not on origin/main' check

echo "check: the other ways a pin is wrong"
pin "$b2"
printf 'tampered\n' > docs/openapi/openapi.yaml
refused "a copy that differs from its pin" 'differs from .* Run: .*pull '"$b2" check
pin "$b2"
rm docs/openapi/openapi.yaml
refused "a missing copy" 'openapi.yaml is missing' check
printf 'v1.3.0-beta\n' > docs/openapi/SOURCE
refused "a ref instead of a full sha" 'not a full 40-character commit sha' check
rm docs/openapi/SOURCE
refused "a missing SOURCE" 'SOURCE is missing' check
pin "$b2"
printf '%s\n' "$(printf 'f%.0s' $(seq 40))" > docs/openapi/SOURCE
refused "a sha origin has never heard of" 'cannot fetch' check

echo "stale"
ok "the copy matches the newest release tag (a prerelease tag is ignored)" pull v1.3.0
ok "  stale agrees" stale
ok "the copy is newer than the release tag, so it differs" pull v1.3.0-beta
refused "  stale warns that it is older than v1.3.0" '::warning::openapi.yaml is older than v1.3.0' stale
git remote set-url origin "$tmp/missing.git"
refused "an origin that cannot be asked fails, not 'no release tag'" 'cannot list tags on origin' stale
git init -q --bare "$tmp/empty.git"
git remote set-url origin "$tmp/empty.git"
ok "an origin with no release tag has nothing to compare" stale
git remote set-url origin "$origin"

exit "$fail"
