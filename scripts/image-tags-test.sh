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

# The property the cases above exist to protect, asserted directly so it
# cannot be lost by someone editing a case.
for pre in v1.3.0-beta v2.0.0-rc.1 v1.0.0-alpha.2; do
  if "$here/image-tags.sh" "$image" tag "$pre" | grep -q ':latest$'; then
    printf '  FAIL prerelease %s moved :latest\n' "$pre"
    fail=1
  fi
done

[ $fail -eq 0 ] || { echo "image tag logic is wrong"; exit 1; }
echo "image tag logic correct"
