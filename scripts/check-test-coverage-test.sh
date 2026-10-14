#!/usr/bin/env bash
# Asserts that check-test-coverage.sh does not call a package with tests
# "untested" because of how git and grep happen to be scheduled.
#
# The bug (#478): pkg_has_any_test ran
#
#   git ls-tree ... | grep -qE "$pattern"
#
# under `set -o pipefail`. grep -q exits on the first match; if git is still
# writing a listing bigger than the pipe buffer it gets SIGPIPE, pipefail turns
# that into a failure of the whole pipeline although grep matched, and the guard
# reported "has no test files at all" for a package that has tests. It is a
# race, so it fails on some runs and not others: a rerun hides it, which is
# why one reproduction passed once in five.
#
# So this test does not run the guard once. It builds a package whose listing
# is far larger than a pipe buffer, with a test file that sorts first (grep
# exits as early as it can) and one that sorts last, and runs the guard many
# times. Any single "has no test files" is a failure.
#
# A control package with the same listing and no test file must still be
# reported, so a guard that stopped reporting anything would not pass.
#
# Usage: scripts/check-test-coverage-test.sh   (RUNS=n to change the 50 runs)
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
guard="$here/check-test-coverage.sh"
runs="${RUNS:-50}"
files="${FILES:-4000}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Padding makes each listing line about 70 bytes, so 4000 files is ~280 KB:
# several times a 64 KiB pipe buffer, which is what forces git to still be
# writing after grep has already seen the first match.
pad='_padding_to_make_the_directory_listing_long_enough'

git -C "$tmp" init -q -b main
git -C "$tmp" config user.email t@example.test
git -C "$tmp" config user.name t
git -C "$tmp" config commit.gpgsign false

mkdir -p "$tmp/backend/internal/tested" "$tmp/backend/internal/untested"
for d in tested untested; do
  dir="$tmp/backend/internal/$d"
  for i in $(seq 1 "$files"); do
    printf 'package %s\n' "$d" >"$dir/$(printf 'file_%05d%s.go' "$i" "$pad")"
  done
  printf 'package %s\n' "$d" >"$dir/handler.go"
done
# One test file that sorts before every other entry and one that sorts after.
printf 'package tested\n' >"$tmp/backend/internal/tested/0000_first_test.go"
printf 'package tested\n' >"$tmp/backend/internal/tested/zzzz_last_test.go"

git -C "$tmp" add -A
git -C "$tmp" commit -q -m base
git -C "$tmp" branch base

for d in tested untested; do
  printf 'func added() {}\n' >>"$tmp/backend/internal/$d/handler.go"
done
git -C "$tmp" commit -q -am change

fail=0
miss=0 # runs that wrongly said a tested package has no tests
lost=0 # runs that failed to say an untested package has none

for _ in $(seq 1 "$runs"); do
  out="$(cd "$tmp" && bash "$guard" base HEAD 2>&1 || true)"
  if grep -q "'backend/internal/tested' has no test files at all" <<<"$out"; then
    miss=$((miss + 1))
  fi
  if ! grep -q "'backend/internal/untested' has no test files at all" <<<"$out"; then
    lost=$((lost + 1))
  fi
done

if [ "$miss" -eq 0 ]; then
  printf '  ok   a package with tests is never reported as having none (%d runs)\n' "$runs"
else
  printf '  FAIL a package with tests was reported as having none in %d of %d runs\n' "$miss" "$runs"
  fail=1
fi
if [ "$lost" -eq 0 ]; then
  printf '  ok   a package without tests is still reported (%d runs)\n' "$runs"
else
  printf '  FAIL a package without tests went unreported in %d of %d runs\n' "$lost" "$runs"
  fail=1
fi

[ $fail -eq 0 ] || { echo "check-test-coverage.sh is unreliable"; exit 1; }
echo "check-test-coverage.sh is reliable"
