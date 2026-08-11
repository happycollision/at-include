#!/usr/bin/env bash
#
# Enforces that any push range containing at least one commit either adds a
# Changie fragment under .changes/unreleased/ OR touches CHANGELOG.md.
# Shared by lefthook's pre-push hook (fast, local, skippable) and a CI step
# on master pushes (after-the-fact backstop) — see lefthook.yml and
# .github/workflows/ci.yml.
#
# The CHANGELOG.md clause exists for release commits: `mise run changelog`
# CONSUMES the unreleased fragments (deletes them, adds .changes/<version>.md,
# and creates/updates CHANGELOG.md), so a release push adds nothing under
# .changes/unreleased/ and would otherwise fail this check — blocking the
# documented `git push origin master vX.Y.Z` release step. This mirrors
# Changie's own gate (miniscruff/changie's changelog-check.yml passes on
# `.changes/unreleased/*.yaml` OR `CHANGELOG.md`), which is exactly how
# their release commits satisfy their own check. CHANGELOG.md uses
# --diff-filter=AM (added or modified) because the file is born at the
# first release and modified by every one after.
#
# Deliberately checks "was a fragment added by this range" rather than "does
# any fragment currently exist", because an unreleased fragment from an
# earlier push stays on disk until the next `changie batch` — a snapshot
# check would pass on a stale leftover fragment and miss a later push that
# forgot its own.
#
# A "None"-kind fragment satisfies this just as well as any other kind: it
# is still a real added file under .changes/unreleased/, so pushing one is a
# deliberate, visible choice to skip the changelog rather than an omission.
#
# Usage: check-changelog-fragment.sh <repo-dir> <before-sha> <after-sha>
#
# <before-sha> may be the all-zeros SHA (new branch/first push) — in that
# case there is nothing upstream to diff against, so the check passes.
# <after-sha> all-zeros means the ref is being DELETED — no commits are
# being added, so there is nothing to check and the check passes.
#
# A non-zero SHA that doesn't resolve to a commit locally (verified: git
# exits 128 with "fatal: Invalid revision range" if handed to rev-list/diff
# directly) is caught up front and turned into an explicit failure with an
# explanation, instead of leaking git's fatal — which never mentions
# fragments and reads like the check itself is broken. The realistic cause
# is a force-push to master: locally the remote's old tip may not exist in
# your clone; in CI, github.event.before is unreachable after a force-push
# even with fetch-depth: 0. Deliberately fails CLOSED: passing here would
# let any force-push bypass the gate entirely.

set -euo pipefail

repo_dir="${1:?usage: check-changelog-fragment.sh <repo-dir> <before-sha> <after-sha>}"
before_sha="${2:?usage: check-changelog-fragment.sh <repo-dir> <before-sha> <after-sha>}"
after_sha="${3:?usage: check-changelog-fragment.sh <repo-dir> <before-sha> <after-sha>}"

zero_sha="0000000000000000000000000000000000000000"

if [[ "$before_sha" == "$zero_sha" ]]; then
  echo "check-changelog-fragment: new ref, nothing to diff against, skipping"
  exit 0
fi

if [[ "$after_sha" == "$zero_sha" ]]; then
  echo "check-changelog-fragment: ref deletion, no commits added, skipping"
  exit 0
fi

for sha in "$before_sha" "$after_sha"; do
  if ! git -C "$repo_dir" rev-parse --quiet --verify "$sha^{commit}" >/dev/null; then
    echo "check-changelog-fragment: FAIL"
    echo "  cannot resolve $sha to a commit in this repository, so the pushed"
    echo "  range cannot be checked for a changelog fragment. The usual cause is"
    echo "  a force-push (the old tip no longer exists in the available history)."
    echo "  This check fails closed: verify by hand that the push added a"
    echo "  fragment under .changes/unreleased/ (or touched CHANGELOG.md for a"
    echo "  release), and avoid force-pushing master."
    exit 1
  fi
done

range="$before_sha..$after_sha"

commit_count="$(git -C "$repo_dir" rev-list --no-merges --count "$range")"
if [[ "$commit_count" -eq 0 ]]; then
  echo "check-changelog-fragment: no non-merge commits in range, skipping"
  exit 0
fi

added_fragments="$(git -C "$repo_dir" diff --name-only --diff-filter=A "$range" -- .changes/unreleased/)"
if [[ -n "$added_fragments" ]]; then
  echo "check-changelog-fragment: OK ($added_fragments)"
  exit 0
fi

changelog_touched="$(git -C "$repo_dir" diff --name-only --diff-filter=AM "$range" -- CHANGELOG.md)"
if [[ -n "$changelog_touched" ]]; then
  echo "check-changelog-fragment: OK (release shape: $changelog_touched touched)"
  exit 0
fi

echo "check-changelog-fragment: FAIL"
echo "  $commit_count commit(s) in range $range add no file under .changes/unreleased/"
echo "  and do not touch CHANGELOG.md (a release commit would)."
echo "  Add a changelog fragment: mise exec -- changie new"
echo "  (If this change genuinely needs no changelog entry, use: changie new --kind None)"
exit 1
