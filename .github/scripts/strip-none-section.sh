#!/usr/bin/env bash
#
# Strips a "### None" heading and everything under it (up to the next "##"
# or "###" heading, or end of file) from a single Changie-rendered version
# file, in place.
#
# Exists because no Changie config field removes a kind's section from
# rendered output — skipGlobalChoices, skipBody, and an empty per-kind
# changeFormat were each tried against the real changie 1.25.2 binary and
# none suppress the heading. The None kind's fragment still exists on disk
# (satisfying the "a fragment was added" push check), but its heading must
# be removed from the rendered file before it's merged into CHANGELOG.md.
#
# Only operates on the single file passed in — never CHANGELOG.md itself —
# so already-merged historical sections are never touched.
#
# Usage: strip-none-section.sh <path-to-version-file>

set -euo pipefail

target="${1:?usage: strip-none-section.sh <path-to-version-file>}"

# Clean up the scratch file on any exit path. Without this, a failure
# partway through (e.g. awk erroring because $target doesn't exist) leaves
# an empty "$target.tmp" on disk — and since this script's real caller
# points it at a file inside a tracked directory (.changes/<version>.md),
# that litter could get accidentally committed.
trap 'rm -f "$target.tmp"' EXIT

if [[ ! -f "$target" ]]; then
  printf 'strip-none-section.sh: no such file: %s\n' "$target" >&2
  exit 1
fi

awk '
  /^### None$/ { skipping = 1; next }
  skipping && /^#/ { skipping = 0 }
  !skipping { print }
' "$target" > "$target.tmp"

mv "$target.tmp" "$target"
