#!/usr/bin/env bash
#
# Post-release consumer smoke test.
#
# Verifies a *published* at-include release the way a real consumer meets it:
# installed from its GitHub release archive by mise, not built from this
# repo's source. That covers ground the main test suite structurally cannot,
# because the main suite always compiles the binary itself — a release archive
# whose name doesn't match what mise expects, a missing or misnamed asset, a
# platform archive that failed to upload, or version metadata lost between
# goreleaser and the shipped binary.
#
# Usage: release-smoke-test.sh <expected-version>
#
# <expected-version> is the release version WITHOUT a leading "v" (e.g.
# "0.1.1"). The caller is responsible for stripping it — see the workflow.
#
# Assumes `at-include` is already on PATH (the workflow installs it via mise
# before calling this). Installing is deliberately not this script's job: the
# install log is where checksum / attestation / SLSA-provenance verification
# becomes visible, and that belongs in its own workflow step so a failure
# there is distinguishable from a behavioral failure here.

set -euo pipefail

expected_version="${1:?usage: release-smoke-test.sh <expected-version>}"

# Assertion bookkeeping. Every check appends to a running tally rather than
# exiting at the first failure, so one run reports everything that's wrong
# with a release instead of only the earliest symptom.
failures=0

pass() { printf 'ok   %s\n' "$1"; }

fail() {
  printf 'FAIL %s\n' "$1"
  shift
  # Remaining args are detail lines, indented under the failure.
  for line in "$@"; do printf '       %s\n' "$line"; done
  failures=$((failures + 1))
}

# assert_eq <label> <expected> <actual>
assert_eq() {
  if [ "$2" = "$3" ]; then
    pass "$1"
  else
    fail "$1" "expected: $2" "actual:   $3"
  fi
}

# Run a command, capturing its exit code without tripping `set -e`.
# Prints the command's own output (stdout+stderr) so CI logs show what the
# released binary actually said, then asserts on the exit code.
#
# assert_exit <label> <expected-code> <cmd...>
assert_exit() {
  local label="$1" want="$2"
  shift 2
  local out got
  out="$("$@" 2>&1)" && got=0 || got=$?
  if [ -n "$out" ]; then printf '%s\n' "$out" | sed 's/^/     | /'; fi
  assert_eq "$label" "$want" "$got"
}

# --- Fixture ----------------------------------------------------------------
#
# Built in a scratch dir rather than committed: it must carry no version
# number anywhere (that staleness is the whole reason this replaced the
# checked-in examples/consumer-smoke-test/), and it is small enough that a
# heredoc is clearer than a directory of files to cross-reference.

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT
cd "$workdir"
mkdir -p notes

cat > AGENTS.src.md <<'EOF'
# Consumer smoke test

@notes/setup.md

Things that must stay literal: `@notes/setup.md` in a code span,
an email like dev@example.com, and a package such as @scope/pkg.

```
@notes/setup.md
```
EOF

# notes/setup.md imports "@shared.md" with no directory prefix, so it only
# resolves if imports are resolved relative to the *importing* file rather
# than the root — the nested-relative behavior this asserts.
cat > notes/setup.md <<'EOF'
## Setup

Run `mise install`.

@shared.md
EOF

printf 'Shared note, imported from a nested file.\n' > notes/shared.md

printf '\n=== at-include release smoke test (expecting %s) ===\n\n' "$expected_version"

# --- 1. Version reports the released version, not "dev" ---------------------
#
# This is the assertion that proves goreleaser's -ldflags version injection
# survived into the shipped binary. A binary built without it reports "dev",
# which is precisely the regression the main suite can't catch.
version_out="$(at-include --version)"
printf '     | %s\n' "$version_out"
assert_eq "--version reports the released version" \
  "at-include ${expected_version}" "$version_out"

# A bare invocation names no command, which is a usage error (exit 2) rather
# than a default action — the subcommand CLI's front door.
assert_exit "bare invocation is a usage error" 2 at-include

# --- 2. A build run produces correct output ---------------------------------
assert_exit "build exits 0" 0 at-include build

generated="$(cat AGENTS.md)"

# Nested imports resolved, each relative to the file that imported it.
# shellcheck disable=SC2016 # the backticks are literal Markdown being matched, not a subshell
if printf '%s' "$generated" | grep -q 'Run `mise install`' &&
  printf '%s' "$generated" | grep -q 'Shared note, imported from a nested file\.'; then
  pass "nested imports resolve relative to the importing file"
else
  fail "nested imports resolve relative to the importing file" \
    "notes/setup.md and/or notes/shared.md content missing from AGENTS.md"
fi

# Both inline-code and fenced occurrences of @notes/setup.md must survive
# verbatim. The generated file contains the *expanded* import too, so count
# the literal token: one in the code span, one in the fence.
literal_count="$(grep -c '@notes/setup\.md' AGENTS.md || true)"
assert_eq "inline code span and fenced block pass through verbatim" 2 "$literal_count"

# Emails and scoped package names are not imports and must not be touched.
if printf '%s' "$generated" | grep -q 'dev@example\.com' &&
  printf '%s' "$generated" | grep -q '@scope/pkg'; then
  pass "emails and @scope/pkg mentions stay literal"
else
  fail "emails and @scope/pkg mentions stay literal" \
    "dev@example.com and/or @scope/pkg were altered"
fi

# --- 3. The other two subcommands run at all --------------------------------
#
# Shallow on purpose: the main suite covers what they emit. What a release
# archive can break — and this can't get from source — is a subcommand that
# isn't wired up in the shipped binary at all. `supplement` defaults --src to
# AGENTS.md, which only exists now that build has run.
assert_exit "supplement exits 0" 0 at-include supplement
assert_exit "imports exits 0" 0 at-include imports

# --- 4. check is a working CI gate ------------------------------------------
assert_exit "check exits 0 when fresh" 0 at-include check

# Editing an imported file (not the source) makes the output stale — the
# realistic way a repo drifts, and the case a naive mtime check would miss.
printf 'Shared note, EDITED.\n' > notes/shared.md
assert_exit "check exits 1 when stale" 1 at-include check

# The regeneration command check prints must actually work. Extract it from
# the message ("<out> is out of date. Run: <cmd>") and run it, rather than
# hardcoding an assumption about what it says.
#
# `at-include check` exits 1 here (the output is still stale, by design),
# and under `set -o pipefail` that failure would propagate out of the command
# substitution and kill the script via `set -e`. `|| true` on the producing
# command keeps the pipeline's status zero while still capturing its stdout.
regen_cmd="$( { at-include check || true; } | sed -n 's/^.* is out of date\. Run: //p' | head -1)"
if [ -z "$regen_cmd" ]; then
  fail "check prints a regeneration command" "no 'Run:' line found in check output"
else
  printf '     | regeneration command: %s\n' "$regen_cmd"
  # shellcheck disable=SC2086 # intentional word splitting: this is a command line
  if $regen_cmd >/dev/null 2>&1 && at-include check >/dev/null 2>&1; then
    pass "the regeneration command check prints actually works"
  else
    fail "the regeneration command check prints actually works" \
      "running '$regen_cmd' did not bring AGENTS.md up to date"
  fi
fi

# --- 5. The --out == --src guard ------------------------------------------
#
# Exits 2 (usage error) and leaves the hand-authored source byte-for-byte
# intact. Compare a hash rather than trusting the exit code alone: the point
# of the guard is that the source file survives.
before="$(cksum < AGENTS.src.md)"
assert_exit "--out equal to --src exits 2" 2 \
  at-include build --src AGENTS.src.md --out AGENTS.src.md
after="$(cksum < AGENTS.src.md)"
assert_eq "--out equal to --src leaves the source untouched" "$before" "$after"

# --- Summary ---------------------------------------------------------------
printf '\n'
if [ "$failures" -ne 0 ]; then
  printf '=== %d assertion(s) failed ===\n' "$failures"
  exit 1
fi
printf '=== all assertions passed ===\n'
