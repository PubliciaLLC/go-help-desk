#!/usr/bin/env bash
# Works out which registry tags a push should publish.
#
# Extracted as a script rather than inlined in the workflow so it can be run
# and asserted on, which matters because getting it wrong is how a beta ends
# up as :latest on somebody's production instance.
set -euo pipefail

image="$1"      # e.g. ghcr.io/publiciallc/go-help-desk
ref_type="$2"   # branch | tag
ref_name="$3"   # v1.3.0 | v1.3.0-beta | the branch name

if [ "$ref_type" = "branch" ]; then
  # A moving build for testers. Unversioned on purpose: it is overwritten by
  # every push, so it is not a thing to run an instance on.
  echo "${image}:edge"
  exit 0
fi

version="${ref_name#v}"

# The shape is checked, not assumed.
#
# The first version of this decided "is this a release, and therefore does it
# move :latest" on one test: does the string contain a hyphen. That is not the
# same question. The workflow triggers on `tags: ['v*']`, so ANY tag without a
# hyphen reached the release branch and moved :latest — `vHotfix` and `vFoo`
# did, and so did `v1.4.0rc1`, which is somebody tagging a release candidate
# and forgetting the hyphen semver wants before the identifier. All three are
# legal Docker tag strings, so the push succeeded and :latest silently landed
# on an untested build. Every instance following :latest would have pulled it.
#
# The tests did not catch it because they only ever fed this well-formed
# input: five well-formed cases and a prerelease loop over well-formed
# prereleases. The property was asserted and the violating inputs were never
# tried. Found by the session-B review of #313.
#
# So: a release is X.Y.Z and nothing else, a prerelease is X.Y.Z-identifier,
# and anything else is refused rather than guessed at. Refusing fails the
# workflow before it authenticates, so nothing is published — the safe
# direction for a tag nobody anticipated.
refuse() {
  echo "image-tags: refusing to publish for tag '${ref_name}'." >&2
  echo "  '${version}' is neither X.Y.Z nor X.Y.Z-identifier." >&2
  echo "  A release tag moves :latest, so an unrecognised shape is not guessed at." >&2
  echo "  Tag as v1.3.0 for a release, or v1.3.0-rc.1 for a prerelease." >&2
  exit 1
}

case "$version" in
  *-*)
    # A prerelease publishes under its own exact name and NOTHING else: no
    # major.minor, and above all no :latest. Somebody running :latest must
    # never be moved onto a beta by a tag push.
    printf '%s' "$version" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+-[0-9A-Za-z.-]+$' || refuse
    echo "${image}:${version}"
    ;;
  *)
    # Build metadata (+build.5) lands here, and is refused for two reasons:
    # '+' is not a legal Docker tag character, and ${version%.*} would compute
    # the major.minor from inside the metadata suffix.
    printf '%s' "$version" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || refuse
    echo "${image}:${version}"
    echo "${image}:${version%.*}"
    echo "${image}:latest"
    ;;
esac
