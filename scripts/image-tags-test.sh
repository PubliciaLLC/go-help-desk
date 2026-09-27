#!/usr/bin/env bash
# Asserts what image-tags.sh publishes for each kind of push.
#
# Run by the release workflow before it pushes anything. The case worth the
# whole script is "a prerelease must not move :latest": getting that wrong
# moves every instance tracking :latest onto a beta, and it is unrecoverable
# in the sense that people have already pulled it.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
image=ghcr.io/publiciallc/go-help-desk
fail=0

check() {
  local desc="$1" want="$2"; shift 2
  local got
  got="$("$here/image-tags.sh" "$@" | tr '\n' ' ' | sed 's/ *$//')"
  want="$(printf '%s' "$want" | sed 's/ *$//')"
  if [ "$got" = "$want" ]; then
    printf '  ok   %s\n' "$desc"
  else
    printf '  FAIL %s\n       got  [%s]\n       want [%s]\n' "$desc" "$got" "$want"
    fail=1
  fi
}

check "a release tag publishes version, major.minor and latest" \
  "${image}:1.3.0 ${image}:1.3 ${image}:latest" "$image" tag v1.3.0
check "a patch release moves major.minor and latest" \
  "${image}:1.2.4 ${image}:1.2 ${image}:latest" "$image" tag v1.2.4
check "a beta publishes its own name and nothing else" \
  "${image}:1.3.0-beta" "$image" tag v1.3.0-beta
check "a release candidate likewise" \
  "${image}:2.0.0-rc.1" "$image" tag v2.0.0-rc.1
check "a branch push publishes only the moving edge tag" \
  "${image}:edge" "$image" branch v1.3.0-beta

# A tag whose shape we do not recognise is refused, not guessed at.
#
# These are the cases the first version got wrong, and the reason it got them
# wrong is that this file only ever fed it well-formed input. It asserted the
# property over v1.3.0-beta and friends and never tried anything else, so the
# suite passed while `vHotfix`, `vFoo` and `v1.4.0rc1` all moved :latest.
#
# v1.4.0rc1 is the realistic one: somebody tags a release candidate and
# forgets the hyphen semver wants before the identifier. It is a legal Docker
# tag, so the push succeeded and :latest landed on an untested build.
#
# Found by the session-B review of #313.
refused() {
  local desc="$1" tag="$2"
  if "$here/image-tags.sh" "$image" tag "$tag" >/dev/null 2>&1; then
    printf '  FAIL %s: %s was accepted\n' "$desc" "$tag"
    fail=1
  else
    printf '  ok   %s\n' "$desc"
  fi
}

refused "a release candidate missing its hyphen is refused"   v1.4.0rc1
refused "a word instead of a version is refused"              vHotfix
refused "another word is refused"                             vFoo
refused "build metadata is refused (+ is not a legal tag)"    v1.3.0+build.5
refused "a two-part version is refused"                       v1.3
refused "a four-part version is refused"                      v1.3.0.1
refused "an empty prerelease identifier is refused"           v1.3.0-

# The property every case above exists to protect, asserted directly so it
# cannot be lost by someone editing a case — and now over malformed input as
# well as well-formed, which is the gap that let the bug through.
for pre in v1.3.0-beta v2.0.0-rc.1 v1.0.0-alpha.2; do
  if "$here/image-tags.sh" "$image" tag "$pre" | grep -q ':latest$'; then
    printf '  FAIL prerelease %s moved :latest\n' "$pre"
    fail=1
  fi
done
for bad in v1.4.0rc1 vHotfix vFoo vLatest v1 release-1.3.0; do
  if "$here/image-tags.sh" "$image" tag "$bad" 2>/dev/null | grep -q ':latest$'; then
    printf '  FAIL unrecognised tag %s moved :latest\n' "$bad"
    fail=1
  fi
done

[ $fail -eq 0 ] || { echo "image tag logic is wrong"; exit 1; }
echo "image tag logic correct"
