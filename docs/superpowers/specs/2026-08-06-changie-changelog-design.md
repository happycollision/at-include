# Changie-based curated changelogs

> **Historical design document.** The shipped implementation diverges from
> this spec in several small ways beyond the one "(Post-design revision:
> …)" note below — e.g. the final `.changie.yaml` (bullet style, `None`
> handled via `auto: none`, a `newlines:` block) and script paths
> (`.github/scripts/`, not `scripts/`). The code and config in the repo are
> authoritative; this document records the design rationale, not the
> current state.

## Problem

Releases currently get their changelog from GoReleaser's `changelog:` block
([.goreleaser.yaml:63-69](../../../.goreleaser.yaml)), which walks raw commit
subjects since the last tag and filters a few conventional-commit prefixes.
Commit messages happen to follow Conventional Commits today only because
that's the convention the AI models authoring most commits default to — it
isn't a rule this project has committed to, and commit subjects make a poor
changelog on their own (they're written for the commit log, not for a reader
deciding whether to upgrade).

The goal: a human-curated (or Claude-curated, on Claude's behalf) changelog
entry per PR/push, decoupled from commit message format, compiled into a
real `CHANGELOG.md` and used as the GitHub Release body — with a safety net
that catches a push to `master` that forgot to add one.

## Tool: Changie

[Changie](https://changie.dev) is a Go CLI (available via `mise`, latest
`1.25.2`) that manages a changelog as a directory of small YAML fragment
files (`.changes/unreleased/*.yaml`), one per change, each naming a "kind"
(Added/Fixed/etc.) and a free-text body. At release time, `changie batch
<version>` compiles all unreleased fragments into a new dated section,
and `changie merge` writes/updates the top-level `CHANGELOG.md` from all
batched versions.

This fits because:
- It's a real Go binary, trivially added to `mise.toml` alongside
  `goreleaser`, `golangci-lint`, etc. — no new language/runtime dependency.
- The fragment-per-change model gives a natural per-PR authoring point and a
  natural "does a fragment exist" check for a merge-safety-net hook.
- `changie batch` output composes with GoReleaser's `--release-notes` flag
  (confirmed: `--release-notes` "will skip GoReleaser changelog generation"),
  so the GitHub Release body can be Changie's curated text instead of
  GoReleaser's commit-derived one, without forking or wrapping GoReleaser.

## Non-goals

- Not adopting or enforcing Conventional Commits. Commit message format is
  unrelated to this design; the changelog fragment is the source of truth.
- Not turning on branch protection or forcing all changes through PRs. Direct
  pushes to `master` remain possible; see "Enforcement model" below for why.
- Not automating *when* to release or removing the human from the loop — a
  human (or Claude, instructed to) still decides each fragment's `kind` at
  authoring time, and still runs the release step and reviews its diff
  before tagging. The resulting version number itself can be computed from
  those kinds (`changie batch auto`) rather than chosen by hand, but that's
  a convenience on top of curation, not a replacement for it.

## Configuration: `.changie.yaml`

Standard Keep-a-Changelog-style kinds, plus a `None` kind for changes that
deliberately need no changelog entry. Each real kind carries an `auto:`
bump level (`major`/`minor`/`patch`) — a genuine Changie feature, confirmed
against the installed `1.25.2` binary — so `changie batch auto` can compute
the next version from whatever kinds are present in `.changes/unreleased/`
instead of always requiring an explicit version:

```yaml
changesDir: .changes
unreleasedDir: unreleased
headerPath: header.tpl.md
changelogPath: CHANGELOG.md
versionExt: md
versionFormat: "## {{.Version}} - {{.Time.Format \"2006-01-02\"}}"
kindFormat: "### {{.Kind}}"
changeFormat: "- {{.Body}}"
kinds:
  - label: Added
    auto: minor
  - label: Changed
    auto: major
  - label: Deprecated
    auto: minor
  - label: Removed
    auto: major
  - label: Fixed
    auto: patch
  - label: Security
    auto: patch
  - label: None
    skipGlobalChoices: true
```

`changie batch` renders a version section grouping fragments by kind, in the
kind order listed above.

(Post-design revision: the shipped `.changie.yaml` maps `Changed` and
`Removed` to `auto: minor`, not `major` — Keep-a-Changelog's "Changed" is
not "breaking", and changie applies no 0.x special-casing, so a single
major-mapped fragment would auto-bump this pre-1.0 project straight to
v1.0.0. See the comment in `.changie.yaml`.)

**Verified against the real `1.25.2` binary (not assumed from docs):**
`KindConfig` has no field that removes a kind from rendering. The candidates
that looked plausible — `skipGlobalChoices`, `skipBody`, an empty per-kind
`changeFormat` — were each tried against a real `.changie.yaml` and a real
`changie batch` run:

- `skipGlobalChoices`/`skipBody` only affect the interactive `changie new`
  prompt flow; they have no effect on `batch` output.
- A per-kind `changeFormat: ""` override **does** apply (confirmed with a
  marker string) but only replaces the bullet line's content — the
  `### None` heading itself comes from the separate global `kindFormat`
  template and renders unconditionally whenever any fragment of that kind
  is present in the batch, empty `changeFormat` or not.

So there is no config-only way to drop the `None` section from
`CHANGELOG.md`. The `changelog` mise task (below) pipes `changie batch`'s
output through a small filter step that strips the `### None` heading and
everything under it up to the next `###`/`##` heading, from the
newly-generated version file only (never touching already-batched
historical sections). This is the actual mechanism, not a fallback.

## Fragment authoring workflow

Per the earlier decision, Claude authors the fragment as part of finishing a
unit of work (typically: right before requesting review / wrapping up a
branch), by running `changie new` non-interactively (via its `--kind`/body
flags, or by writing the fragment YAML directly — whichever the installed
version's non-interactive flags support) rather than the default interactive
prompt. This will be captured as an instruction in the relevant workflow doc
(e.g. `AGENTS.md`/`CLAUDE.md` or a skill), not as code — out of scope for
this spec's implementation plan itself, but noted here so the enforcement
mechanism below has a producer to pair with.

If a change genuinely needs no changelog entry (e.g. a pure CI config tweak,
a comment fix, an internal refactor with no user-visible effect), Claude
creates a fragment with `kind: None` instead of skipping fragment creation
altogether — this keeps "a fragment exists" a meaningful, single-mechanism
check rather than needing a separate bypass path.

## Enforcement model

Master has no branch protection today, and the project's real history
includes direct local merges/pushes to `master`, not exclusively
GitHub-PR-merge commits. Per the decision to keep master push-able for now
(branch protection is a planned future step, not part of this design), there
is no server-side gate available yet. Two complementary, best-effort layers:

### 1. Lefthook `pre-push` job (fast, local, primary signal)

Added to the existing `pre-push` group in `lefthook.yml` (alongside the
current `check` job). Logic:

1. Determine whether this push updates the `master` ref specifically (a
   pre-push hook receives the list of `<local-ref> <local-sha> <remote-ref>
   <remote-sha>` lines on stdin; only act on lines where remote-ref is
   `refs/heads/master`).
2. For that line, diff the range being pushed —
   `git diff --name-only --diff-filter=A <remote-sha>..<local-sha> --
   .changes/unreleased/` — to find fragment files *added by this push*
   specifically (not a stale fragment left over from a previous push that
   hasn't been batched yet).
3. If the range contains any non-merge commit at all AND that diff is empty,
   fail the push with a clear message pointing at `changie new`.
4. If `<remote-sha>` is the all-zeros value (branch doesn't exist upstream
   yet), skip the check — there's no `master` to diff against in that state,
   and pushing a new branch (not `master` itself) already can't match the
   `refs/heads/master` filter in step 1 anyway.

This is best-effort: skippable via `--no-verify`, and only runs on machines
with `lefthook install` set up locally. That's accepted, per the decision to
treat this as a safety net rather than a hard gate.

### 2. CI check on `push: branches: [master]` (after-the-fact backstop)

A new step in the existing `check` job in `ci.yml`, gated to only run when
`github.event_name == 'push'` (the job also runs on `pull_request`, where
there is no meaningful "before" SHA on `master` to diff against — PRs get no
changelog gate at all yet, consistent with the decision not to force PRs).
On the `push` path, it runs the same range-diff logic against
`github.event.before`/`github.sha` (the push event's before/after SHAs).
This can't prevent a bad push — by the time CI runs, the commit is already
on `master` — but it turns the omission into a visible red CI status on
GitHub rather than silent drift, so it gets noticed and corrected via a
small follow-up commit adding the missed fragment.

Both layers run the *same* rule (range contains a commit → range must
contain an added `.changes/unreleased/*` file, OR add/modify
`CHANGELOG.md`); the shared logic should live in one script (e.g.
`scripts/check-changelog-fragment.sh`) invoked from both `lefthook.yml` and
`ci.yml` rather than duplicated inline in YAML, so the rule only needs to
be gotten right once.

The `CHANGELOG.md` OR-clause is what lets release commits through: `mise
run changelog` consumes fragments (deletes them, adds
`.changes/<version>.md`, creates/updates `CHANGELOG.md`) without adding
anything under `unreleased/`, so without the OR-clause the documented
release push itself would fail the gate. It mirrors Changie's own
convention — miniscruff/changie's `changelog-check.yml` gates on the
file-pattern `.changes/unreleased/*.yaml` OR `CHANGELOG.md` for the same
reason. `CHANGELOG.md` is matched added-or-modified (`--diff-filter=AM`)
because the file doesn't exist until the first release creates it.

## Release scripting

A new `mise` task, `changelog`, run manually before cutting a release. It
batches fragments, then strips the `### None` section from the
newly-generated version file only (see the verified rendering limitation
above), then merges into `CHANGELOG.md`:

```toml
[tasks.changelog]
description = "Batch unreleased Changie fragments into CHANGELOG.md for a version"
run = """
changie batch ${VERSION:-auto}
scripts/strip-none-section.sh .changes/${VERSION:-$(changie latest)}.md
changie merge
"""
```

(`changie batch auto` uses each present kind's `auto:` bump level to compute
the next version itself — see the config above — so `VERSION` can be left
unset for a standard bump, or set explicitly to override.)

Manual release flow becomes:

1. `mise run changelog` — batches `.changes/unreleased/*` into a new version
   file, picking the version itself via each fragment's `auto:` bump level
   (or `VERSION=vX.Y.Z mise run changelog` to pick the version explicitly
   instead). Either way, the version is known only *after* this step: the
   task prints it, and `changie latest` can also report it. Strips the new
   file's `None` section, then regenerates `CHANGELOG.md` from all batched
   version files (also removes the now-batched fragment files from
   `unreleased/`).
2. Review the diff, commit `CHANGELOG.md` and the `.changes/` changes.
3. Using the version from step 1 (call it `vX.Y.Z`): `git tag vX.Y.Z && git
   push origin master vX.Y.Z`, as today — this push also passes through the
   pre-push hook above. It passes not because `unreleased/` is empty (the
   check looks at what the range *adds*, not at current state, so an empty
   directory alone would fail it) but because the release commit
   creates/updates `CHANGELOG.md`, which the check accepts as an
   alternative to an added fragment — see the OR-clause in "Enforcement
   model" above.
4. The existing `release.yml` GitHub Actions workflow fires on the tag push
   as today, but its `goreleaser release --clean` invocation gains a
   `--release-notes=<path>` flag pointing at the just-batched, stripped
   version file (`.changes/vX.Y.Z.md`), so the GitHub Release body is
   Changie's curated text. `--release-notes` is confirmed (via `goreleaser
   release --help`) to skip GoReleaser's own commit-based changelog
   generation entirely, so `.goreleaser.yaml`'s existing `changelog:` block
   is removed rather than kept alongside it (the earlier decision was
   "replace", not "keep both").

## Testing

- Unit-test-equivalent: a fixture-style check that `.changie.yaml` produces
  the expected `CHANGELOG.md` shape from a couple of sample fragments
  (added/fixed/none), run via `changie batch --dry-run` or similar if
  available in `1.25.2`, else a real batch against a scratch copy.
- The shared range-diff script gets its own small test (e.g. a bash test
  harness or a Go test invoking it against a scratch git repo) covering: no
  commits pushed → pass; commits pushed with fragment → pass; commits pushed
  without fragment → fail; new branch push (all-zeros before-SHA) → skip.
- Manually verify end-to-end once: create a real fragment, run the
  `changelog` task, confirm `CHANGELOG.md` renders correctly and
  `goreleaser build --snapshot --release-notes=...` (or equivalent dry run)
  picks it up.
