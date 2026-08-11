#!/usr/bin/env bash
#
# Test harness for check-changelog-fragment.sh. Run directly:
# ./check-changelog-fragment_test.sh

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
target="$script_dir/check-changelog-fragment.sh"
failures=0

pass() { printf 'ok   %s\n' "$1"; }
fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }

# Builds a scratch repo with an initial commit, returns its path on stdout.
new_repo() {
  local dir
  dir="$(mktemp -d)"
  git -C "$dir" init -q
  git -C "$dir" config user.email "test@example.com"
  git -C "$dir" config user.name "Test"
  mkdir -p "$dir/.changes/unreleased"
  touch "$dir/.changes/unreleased/.gitkeep"
  git -C "$dir" add .
  git -C "$dir" commit -q -m "initial"
  echo "$dir"
}

# Case 1: range has a commit, no fragment added -> fail
repo="$(new_repo)"
before="$(git -C "$repo" rev-parse HEAD)"
echo "change" >> "$repo/somefile.txt"
git -C "$repo" add somefile.txt
git -C "$repo" commit -q -m "a change with no fragment"
after="$(git -C "$repo" rev-parse HEAD)"
if "$target" "$repo" "$before" "$after" >/dev/null 2>&1; then
  fail "commit without fragment should fail the check"
else
  pass "commit without fragment fails the check"
fi
rm -rf "$repo"

# Case 2: range has a commit, fragment added -> pass
repo="$(new_repo)"
before="$(git -C "$repo" rev-parse HEAD)"
echo "change" >> "$repo/somefile.txt"
echo "kind: Fixed" > "$repo/.changes/unreleased/fixed-1.yaml"
git -C "$repo" add somefile.txt .changes/unreleased/fixed-1.yaml
git -C "$repo" commit -q -m "a change with a fragment"
after="$(git -C "$repo" rev-parse HEAD)"
if "$target" "$repo" "$before" "$after" >/dev/null 2>&1; then
  pass "commit with fragment passes the check"
else
  fail "commit with fragment should pass the check"
fi
rm -rf "$repo"

# Case 3: range has no commits at all (before == after) -> pass (nothing to check)
repo="$(new_repo)"
same="$(git -C "$repo" rev-parse HEAD)"
if "$target" "$repo" "$same" "$same" >/dev/null 2>&1; then
  pass "empty range passes the check"
else
  fail "empty range should pass the check"
fi
rm -rf "$repo"

# Case 4: before-sha is all-zeros (new branch) -> pass (nothing to diff against)
repo="$(new_repo)"
after="$(git -C "$repo" rev-parse HEAD)"
zeros="0000000000000000000000000000000000000000"
if "$target" "$repo" "$zeros" "$after" >/dev/null 2>&1; then
  pass "all-zeros before-sha passes the check"
else
  fail "all-zeros before-sha should pass the check"
fi
rm -rf "$repo"

# Case 5: release-commit shape -> pass. `mise run changelog` DELETES the
# unreleased fragments, ADDS .changes/<version>.md, and ADDS CHANGELOG.md
# (first release) — it adds nothing under .changes/unreleased/, so it must
# be satisfied by the CHANGELOG.md clause instead.
repo="$(new_repo)"
echo "kind: Fixed" > "$repo/.changes/unreleased/fixed-1.yaml"
git -C "$repo" add .changes/unreleased/fixed-1.yaml
git -C "$repo" commit -q -m "a change with a fragment"
before="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" rm -q .changes/unreleased/fixed-1.yaml
echo "## v0.4.0" > "$repo/.changes/v0.4.0.md"
echo "# Changelog" > "$repo/CHANGELOG.md"
git -C "$repo" add .changes/v0.4.0.md CHANGELOG.md
git -C "$repo" commit -q -m "release v0.4.0"
after="$(git -C "$repo" rev-parse HEAD)"
if "$target" "$repo" "$before" "$after" >/dev/null 2>&1; then
  pass "release commit (drains fragments, adds CHANGELOG.md) passes the check"
else
  fail "release commit (drains fragments, adds CHANGELOG.md) should pass the check"
fi
rm -rf "$repo"

# Case 6: subsequent-release shape -> pass. From the second release onward
# CHANGELOG.md already exists, so the release commit MODIFIES it rather than
# adding it — the check must accept both (diff-filter AM, not just A).
repo="$(new_repo)"
echo "# Changelog" > "$repo/CHANGELOG.md"
echo "kind: Fixed" > "$repo/.changes/unreleased/fixed-1.yaml"
git -C "$repo" add CHANGELOG.md .changes/unreleased/fixed-1.yaml
git -C "$repo" commit -q -m "a change with a fragment (CHANGELOG.md already exists)"
before="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" rm -q .changes/unreleased/fixed-1.yaml
echo "## v0.5.0" > "$repo/.changes/v0.5.0.md"
echo "## v0.5.0" >> "$repo/CHANGELOG.md"
git -C "$repo" add .changes/v0.5.0.md CHANGELOG.md
git -C "$repo" commit -q -m "release v0.5.0"
after="$(git -C "$repo" rev-parse HEAD)"
if "$target" "$repo" "$before" "$after" >/dev/null 2>&1; then
  pass "release commit (drains fragments, modifies CHANGELOG.md) passes the check"
else
  fail "release commit (drains fragments, modifies CHANGELOG.md) should pass the check"
fi
rm -rf "$repo"

# Case 7: after-sha is all-zeros (ref deletion) -> pass. Deleting a ref adds
# no commits, so there is nothing to check — and without this special case
# git would exit 128 ("Invalid revision range") instead of giving a verdict.
repo="$(new_repo)"
before="$(git -C "$repo" rev-parse HEAD)"
zeros="0000000000000000000000000000000000000000"
if "$target" "$repo" "$before" "$zeros" >/dev/null 2>&1; then
  pass "all-zeros after-sha (ref deletion) passes the check"
else
  fail "all-zeros after-sha (ref deletion) should pass the check"
fi
rm -rf "$repo"

# Case 8: unresolvable before-sha (force-push shape: the old tip no longer
# exists in available history) -> fail CLOSED, with the check's own message
# rather than git's raw "fatal: Invalid revision range" (exit 128).
repo="$(new_repo)"
after="$(git -C "$repo" rev-parse HEAD)"
bogus="deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
output="$("$target" "$repo" "$bogus" "$after" 2>&1)" && rc=0 || rc=$?
if [[ "$rc" -eq 1 ]] && [[ "$output" == *"force-push"* ]]; then
  pass "unresolvable before-sha fails closed with an explanatory message"
else
  fail "unresolvable before-sha should exit 1 (got $rc) and mention force-push (got: $output)"
fi
rm -rf "$repo"

if [[ "$failures" -gt 0 ]]; then
  printf '%d test(s) failed\n' "$failures"
  exit 1
fi
printf 'all tests passed\n'
