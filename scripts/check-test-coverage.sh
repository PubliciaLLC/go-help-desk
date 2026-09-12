#!/usr/bin/env bash
#
# Flags Go changes that arrive without tests.
#
# CLAUDE.md requires a test before an implementation. This script is the
# mechanical half of that rule: it does not judge whether a test is *good*,
# only whether one plausibly exists for the code being changed. Reviewers still
# have to read the tests.
#
# Usage:
#   scripts/check-test-coverage.sh [base-ref] [head-ref]
#
# Defaults to origin/main...HEAD, so it runs the same way locally as in CI:
#   scripts/check-test-coverage.sh
#
# Exit status:
#   0  no blocking findings (warnings may still be printed)
#   1  at least one blocking finding
#   2  invoked incorrectly (bad refs, not a git repo)

set -euo pipefail

BASE_REF="${1:-origin/main}"
HEAD_REF="${2:-HEAD}"

# Paths exempt from the test requirement. Extended regexes, matched against the
# repo-relative path. Keep this list short and justified — every entry is a
# place where an untested change will not be flagged.
EXEMPT_PATHS=(
  '^backend/internal/dbgen/'      # sqlc-generated; CLAUDE.md: do not hand-edit
  '^backend/internal/testutil/'   # test helpers; tested by the tests that use them
  '^backend/internal/ui/'         # go:embed wrapper, no logic
  '^backend/internal/version/'    # build-stamped constants
  '^backend/cmd/'                 # process wiring; CLAUDE.md accepts verbose honest wiring
)

# ── preflight ────────────────────────────────────────────────────────────────

git rev-parse --git-dir >/dev/null 2>&1 || {
  echo "error: not a git repository" >&2
  exit 2
}

for ref in "$BASE_REF" "$HEAD_REF"; do
  git rev-parse --verify --quiet "$ref" >/dev/null || {
    echo "error: cannot resolve ref '$ref'" >&2
    exit 2
  }
done

# ── GitHub Actions annotation helpers ────────────────────────────────────────
# Outside Actions these degrade to plain prefixed lines.

in_actions() { [[ -n "${GITHUB_ACTIONS:-}" ]]; }

annotate() { # level, file, message
  local level="$1" file="$2" msg="$3"
  if in_actions; then
    printf '::%s file=%s::%s\n' "$level" "$file" "$msg"
  else
    printf '%-7s %s: %s\n' "[${level}]" "$file" "$msg"
  fi
}

# ── collect changed Go files ─────────────────────────────────────────────────

is_exempt() {
  local path="$1" pattern
  for pattern in "${EXEMPT_PATHS[@]}"; do
    [[ "$path" =~ $pattern ]] && return 0
  done
  return 1
}

# Added/Copied/Modified/Renamed only — a deletion needs no test.
mapfile -t changed < <(
  git diff --name-only --diff-filter=ACMR "${BASE_REF}...${HEAD_REF}" -- '*.go' |
    grep -v '_test\.go$' || true
)

# added_lines reports how many lines a change ADDS to a file. A file that only
# loses lines is a pure deletion, even though git records it as "M" rather than
# "D" — removing dead code from a still-live file looks like a modification.
#
# Such a change adds no behaviour, so demanding a test for it is noise. Worse,
# it makes deleting dead code more expensive than leaving it, which is exactly
# backwards: this guard exists to raise the cost of untested code, not to
# protect code nobody calls.
added_lines() {
  local f="$1"
  git diff --numstat "${BASE_REF}...${HEAD_REF}" -- "$f" | awk '{print $1; exit}'
}

declare -a code_files=()
for f in "${changed[@]}"; do
  [[ -n "$f" ]] || continue
  is_exempt "$f" && continue
  # "-" is git's marker for a binary file; treat it as non-zero and check it.
  added="$(added_lines "$f")"
  [[ "$added" == "0" ]] && continue
  code_files+=("$f")
done

if [[ ${#code_files[@]} -eq 0 ]]; then
  echo "No non-exempt Go source changes between ${BASE_REF} and ${HEAD_REF}; nothing to check."
  exit 0
fi

# Test files this change touches, and the packages they belong to.
mapfile -t changed_tests < <(
  git diff --name-only --diff-filter=ACMR "${BASE_REF}...${HEAD_REF}" -- '*_test.go' || true
)

declare -A pkg_has_changed_test=()
for t in "${changed_tests[@]}"; do
  [[ -n "$t" ]] || continue
  pkg_has_changed_test["$(dirname "$t")"]=1
done

# Which changed files are brand new (stricter rule applies to them).
declare -A is_new_file=()
while IFS= read -r f; do
  [[ -n "$f" ]] && is_new_file["$f"]=1
done < <(git diff --name-only --diff-filter=A "${BASE_REF}...${HEAD_REF}" -- '*.go' | grep -v '_test\.go$' || true)

# ── evaluate ─────────────────────────────────────────────────────────────────

blocking=0
warnings=0

# Directories that may hold tests for a given package.
#
# Usually that is just the package's own directory. The exception is the
# database store packages: their tests are integration tests against real
# Postgres and live one level up in backend/internal/database/ (as
# database_test.go or <name>store_test.go), because that is where the shared
# test harness lives. Treat that parent directory as a valid test location for
# them rather than demanding a _test.go inside each store package.
pkg_test_dirs() {
  local pkg="$1"
  echo "$pkg"
  if [[ "$pkg" =~ ^backend/internal/database/[a-z]+store$ ]]; then
    echo "backend/internal/database"
  fi
}

# Does any valid test location for this package contain a test file, at HEAD?
pkg_has_any_test() {
  local pkg="$1" dir
  while IFS= read -r dir; do
    if git ls-tree --name-only "$HEAD_REF" "${dir}/" 2>/dev/null | grep -q '_test\.go$'; then
      return 0
    fi
  done < <(pkg_test_dirs "$pkg")
  return 1
}

# Did this change touch a test in any valid test location for the package?
pkg_test_was_changed() {
  local pkg="$1" dir
  while IFS= read -r dir; do
    [[ -n "${pkg_has_changed_test[$dir]:-}" ]] && return 0
  done < <(pkg_test_dirs "$pkg")
  return 1
}

# Is there a test file named after this specific source file?
has_sibling_test() {
  local file="$1"
  local sibling="${file%.go}_test.go"
  git cat-file -e "${HEAD_REF}:${sibling}" 2>/dev/null
}

declare -A reported_pkg=()

for f in "${code_files[@]}"; do
  pkg="$(dirname "$f")"

  if ! pkg_has_any_test "$pkg"; then
    # Whole package is untested. Report once per package.
    if [[ -z "${reported_pkg[$pkg]:-}" ]]; then
      reported_pkg["$pkg"]=1
      annotate error "$f" \
        "Package '${pkg}' has no test files at all, and this change modifies it. CLAUDE.md requires a test before an implementation. Add a _test.go in ${pkg}."
      blocking=$((blocking + 1))
    fi
    continue
  fi

  if [[ -n "${is_new_file[$f]:-}" ]]; then
    # New source file in a tested package: require a test signal for it
    # specifically, not just that the package was tested before.
    if ! has_sibling_test "$f" && ! pkg_test_was_changed "$pkg"; then
      annotate error "$f" \
        "New file with no tests: neither ${f%.go}_test.go exists nor does this change touch any test in ${pkg}."
      blocking=$((blocking + 1))
      continue
    fi
  fi

  if ! pkg_test_was_changed "$pkg"; then
    annotate warning "$f" \
      "Modified without any test change in ${pkg}. If this changes behaviour, a test should change with it."
    warnings=$((warnings + 1))
  fi
done

# ── report ───────────────────────────────────────────────────────────────────

{
  echo ""
  echo "Test-coverage guard: ${BASE_REF}...${HEAD_REF}"
  echo "  Go source files changed (non-exempt): ${#code_files[@]}"
  echo "  Test files changed:                   ${#changed_tests[@]}"
  echo "  Blocking findings:                    ${blocking}"
  echo "  Warnings:                             ${warnings}"
} >&2

if in_actions && [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo "### Test-coverage guard"
    echo ""
    echo "| | |"
    echo "|---|---|"
    echo "| Go source files changed | ${#code_files[@]} |"
    echo "| Test files changed | ${#changed_tests[@]} |"
    echo "| Blocking findings | ${blocking} |"
    echo "| Warnings | ${warnings} |"
    echo ""
    if [[ $blocking -gt 0 ]]; then
      echo "Blocking findings are listed as annotations on the **Files changed** tab."
      echo ""
      echo "A structural check cannot tell a real test from a compile fix — it only"
      echo "confirms a test plausibly exists. Read the tests before approving."
    fi
  } >>"$GITHUB_STEP_SUMMARY"
fi

if [[ $blocking -gt 0 ]]; then
  echo "" >&2
  echo "FAIL: ${blocking} change(s) land without tests." >&2
  exit 1
fi

echo "" >&2
echo "PASS" >&2
