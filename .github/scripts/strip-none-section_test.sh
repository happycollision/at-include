#!/usr/bin/env bash
#
# Test harness for strip-none-section.sh. Run directly: ./strip-none-section_test.sh

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
target="$script_dir/strip-none-section.sh"
failures=0

pass() { printf 'ok   %s\n' "$1"; }
fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }

run_case() {
  local name="$1" input="$2" expected="$3"
  local tmpfile
  tmpfile="$(mktemp)"
  printf '%s' "$input" > "$tmpfile"
  "$target" "$tmpfile"
  local actual
  actual="$(cat "$tmpfile")"
  rm -f "$tmpfile"
  if [[ "$actual" == "$expected" ]]; then
    pass "$name"
  else
    fail "$name"
    printf '  expected:\n%s\n  actual:\n%s\n' "$expected" "$actual"
  fi
}

run_case "strips None section between two other sections" \
"## v0.1.0 - 2026-08-06
### Added
* Added a real feature
### None
* internal only change
### Fixed
* Fixed a real bug" \
"## v0.1.0 - 2026-08-06
### Added
* Added a real feature
### Fixed
* Fixed a real bug"

run_case "strips None section at the end of the file" \
"## v0.1.0 - 2026-08-06
### Fixed
* Fixed a real bug
### None
* internal only change" \
"## v0.1.0 - 2026-08-06
### Fixed
* Fixed a real bug"

run_case "no-op when there is no None section" \
"## v0.1.0 - 2026-08-06
### Fixed
* Fixed a real bug" \
"## v0.1.0 - 2026-08-06
### Fixed
* Fixed a real bug"

run_case "strips a None section with multiple bullet lines" \
"## v0.1.0 - 2026-08-06
### None
* first internal change
* second internal change
### Fixed
* Fixed a real bug" \
"## v0.1.0 - 2026-08-06
### Fixed
* Fixed a real bug"

if [[ "$failures" -gt 0 ]]; then
  printf '%d test(s) failed\n' "$failures"
  exit 1
fi
printf 'all tests passed\n'
