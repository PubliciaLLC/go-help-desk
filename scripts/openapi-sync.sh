#!/bin/sh
# Keeps docs/openapi/openapi.yaml, the copy the API reference serves, byte-identical
# to docs/api/openapi.yaml at the app commit named on the first line of
# docs/openapi/SOURCE.
#
#   scripts/openapi-sync.sh check        fail unless the copy matches that commit
#   scripts/openapi-sync.sh stale        fail (with a ::warning::) when the newest
#                                        release tag carries a different spec, or
#                                        when origin cannot be asked
#   scripts/openapi-sync.sh pull <ref>   copy the spec at <ref> (branch, tag or
#                                        commit on origin) and rewrite SOURCE
#
# CI (.github/workflows/openapi-sync.yml) runs `check` on every push and pull
# request. Nothing runs `stale` on a schedule: GitHub fires scheduled and manual
# workflows only from the default branch, and this file lives on the website
# branch. `stale` is a manual release-checklist step instead.
#
# At each app release (example for 1.3.0):
#   scripts/openapi-sync.sh pull v1.3.0
#   scripts/openapi-sync.sh check && scripts/openapi-sync.sh stale
#   git commit -am "Re-pin the API reference to v1.3.0"
#
# When upgrading the vendored Redoc (vendor/redoc/<version>/), list the hosts the
# new bundle names, before publishing it:
#   grep -oE 'https?://[A-Za-z0-9.-]+' vendor/redoc/<version>/redoc.standalone.js | sort | uniq -c
# Anything it could load at runtime other than JSON-schema identifiers and links
# (2.5.4: only cdn.redoc.ly, the sidebar logo) needs handling in
# docs/api-reference.html, as the logo shim and the img-src policy there do.
#
# The origin remote must be the app repository, where docs/api/openapi.yaml lives.
set -eu

SPEC_PATH=docs/api/openapi.yaml
COPY=docs/openapi/openapi.yaml
SOURCE=docs/openapi/SOURCE

die() {
  printf '%s\n' "$*" >&2
  exit 1
}

# The commit is the first line of SOURCE. Anything after it is a note.
source_sha() {
  [ -f "$SOURCE" ] || die "$SOURCE is missing"
  sha=$(sed -n 1p "$SOURCE")
  printf '%s' "$sha" | grep -Eq '^[0-9a-f]{40}$' ||
    die "the first line of $SOURCE is not a full 40-character commit sha"
  printf '%s\n' "$sha"
}

# Make sure a commit is present locally. actions/checkout with fetch-depth 0
# already has it; a plain clone may need to ask origin for it by sha.
ensure_commit() {
  git cat-file -e "$1^{commit}" 2>/dev/null ||
    git fetch --no-tags -q origin "$1" ||
    die "cannot fetch $1 from origin"
}

cmd_check() {
  sha=$(source_sha)
  ensure_commit "$sha"
  git cat-file -e "$sha:$SPEC_PATH" 2>/dev/null ||
    die "$SPEC_PATH does not exist at $sha"
  [ -f "$COPY" ] || die "$COPY is missing"
  if git show "$sha:$SPEC_PATH" | cmp -s - "$COPY"; then
    printf 'ok: %s matches %s at %s\n' "$COPY" "$SPEC_PATH" "$sha"
  else
    die "$COPY differs from $SPEC_PATH at $sha. Run: scripts/openapi-sync.sh pull $sha"
  fi
}

cmd_stale() {
  # Newest release tag: vX.Y.Z only, no pre-release suffix, no peeled ^{} entries.
  # Asked separately so that an unreachable origin fails rather than reading as
  # "no release tag" (a pipeline's status is its last command's).
  refs=$(git ls-remote --tags origin 'refs/tags/v*') ||
    die "cannot list tags on origin"
  tag=$(printf '%s\n' "$refs" |
    sed -n 's#.*refs/tags/\(v[^^]*\)$#\1#p' |
    grep -v -- '-' | sort -V | tail -n 1)
  if [ -z "$tag" ]; then
    printf 'no release tag on origin; nothing to compare\n'
    return 0
  fi
  git fetch --no-tags -q origin "refs/tags/$tag:refs/tags/$tag" ||
    die "cannot fetch tag $tag from origin"
  tsha=$(git rev-parse --verify -q "refs/tags/$tag^{commit}") ||
    die "tag $tag does not name a commit"
  if ! git cat-file -e "$tsha:$SPEC_PATH" 2>/dev/null; then
    printf '%s has no %s; nothing to compare\n' "$tag" "$SPEC_PATH"
    return 0
  fi
  if git show "$tsha:$SPEC_PATH" | cmp -s - "$COPY"; then
    printf 'ok: %s matches %s\n' "$COPY" "$tag"
    return 0
  fi
  printf '::warning::openapi.yaml is older than %s. Re-pin docs/openapi/SOURCE and the copy.\n' "$tag"
  exit 1
}

cmd_pull() {
  ref=${1:-}
  [ -n "$ref" ] || die "usage: $0 pull <ref>"
  git fetch --no-tags -q origin "$ref" || die "cannot fetch $ref from origin"
  sha=$(git rev-parse --verify -q 'FETCH_HEAD^{commit}') ||
    die "$ref does not name a commit"
  git cat-file -e "$sha:$SPEC_PATH" 2>/dev/null ||
    die "$SPEC_PATH does not exist at $sha ($ref)"
  git show "$sha:$SPEC_PATH" > "$COPY"
  printf '%s\n# app %s at this commit, taken from ref %s. See scripts/openapi-sync.sh.\n' \
    "$sha" "$SPEC_PATH" "$ref" > "$SOURCE"
  printf 'pulled %s from %s (%s)\n' "$SPEC_PATH" "$ref" "$sha"
}

case "${1:-}" in
  check) cmd_check ;;
  stale) cmd_stale ;;
  pull) shift; cmd_pull "$@" ;;
  *) die "usage: $0 check | stale | pull <ref>" ;;
esac
