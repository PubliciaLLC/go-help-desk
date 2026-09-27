#!/usr/bin/env bash
# Works out which registry tags a push should publish.
#
# Extracted as a script rather than inlined in the workflow so it can be run
# and asserted on, which matters because getting it wrong is how a beta ends
# up as :latest on somebody's production instance.
set -euo pipefail

image="$1"      # e.g. ghcr.io/publiciallc/go-help-desk
ref_type="$2"   # branch | tag
ref_name="$3"   # v1.3.0-beta | v1.3.0 | v1.3.0-beta (branch)

if [ "$ref_type" = "branch" ]; then
  # A moving build for testers. Unversioned on purpose: it is overwritten by
  # every push, so it is not a thing to run an instance on.
  echo "${image}:edge"
  exit 0
fi

version="${ref_name#v}"

# A prerelease carries a hyphen (1.3.0-beta). It publishes under its own exact
# name and NOTHING else: no major.minor, and above all no :latest. Somebody
# running :latest must never be moved onto a beta by a tag push.
case "$version" in
  *-*)
    echo "${image}:${version}"
    ;;
  *)
    echo "${image}:${version}"
    echo "${image}:${version%.*}"
    echo "${image}:latest"
    ;;
esac
