# at-include — agent instructions

`at-include` flattens `@<path>` Markdown imports, following the same
conventions Claude Code uses to inline `@`-referenced files from a `CLAUDE.md`.

**Do not treat parity with Claude Code as the spec.** The resemblance is by
intent, but this is an independent implementation: upstream's import handling is
largely undocumented, some differences are deliberate, and drift over releases
is expected. So when you are tempted to change behavior "to match Claude Code":
this project's own tests and fixtures are the contract, and a divergence is only
a bug if it breaks them or a documented rule in `README.md`. If you do want to
close a gap, establish the upstream behavior empirically first (never guess),
pin the new behavior with tests, and update the known-differences list in
`docs/architecture.md` — don't silently swap one undocumented assumption for
another.

**Output compatibility is what matters** — the generated file's content
(banner, `Contents of X (...)` markers, blank-line placement, single trailing
newline, inline-once/cycle behavior) is pinned by the fixture suite under
`test/cases/` and must not change casually. Internal implementation is
ordinary, idiomatic Go, free to change as long as that user-visible output
stays the same.

@docs/architecture.md

## Conventions this codebase actually follows

See [`docs/conventions.md`](docs/conventions.md) for testing conventions
(`t.Parallel()` usage, fixture-case format, and lint suppression style).

## Build, test, lint

```sh
mise install       # pinned Go toolchain + tools (see mise.toml)
mise run build      # build ./dist/at-include (also injects the version string)
mise run test       # unit tests + the black-box fixture suite under test/
mise run lint       # golangci-lint run
mise run check      # what CI runs: gofumpt -l check, lint, then test
```

## Changelog fragments (one per change, before pushing to master)

Each user-visible change gets its own [Changie](https://changie.dev) fragment
under `.changes/unreleased/`, created in the same commit as the change it
describes:

```sh
mise exec -- changie new --kind <Added|Changed|Deprecated|Removed|Fixed|Security> --body "Reader-facing description of the change"
```

**One fragment per change, not per push.** A branch with three distinct
user-visible changes gets three fragments. The pre-push hook and CI backstop
only verify that *a* fragment was added somewhere in the pushed range, so
they cannot catch a branch that bundled several changes under one entry —
that granularity is your responsibility, and it's what makes the released
changelog useful.

Write the body for someone deciding whether to upgrade, not as a commit
subject: describe what changed for them, not how you implemented it.

If a change genuinely warrants no changelog line (internal refactor, CI
tweak, test-only work), record that decision with `--kind None` rather than
skipping the fragment — and say *why* it's exempt, since that note is the
only record of the judgment call:

```sh
mise exec -- changie new --kind None --body "Test-only: adds fixture coverage for stdin handling, no behavior change."
```

Do not run `mise run changelog` or edit `CHANGELOG.md` yourself. Batching
fragments is the human-driven release step (see "Releasing" in `README.md`),
and touching `CHANGELOG.md` directly would satisfy the push check without
actually recording a change.

## AGENTS.md is generated

This file (`AGENTS.src.md`) is the source. **`AGENTS.md` is generated from it
by `at-include` itself — do not hand-edit `AGENTS.md`.** If you're an agent
and were asked to change `AGENTS.md`, make the change here (or in an imported
file such as `docs/architecture.md`) and regenerate:

```sh
mise run build && ./dist/at-include
```

`mise run docs:check` (or `./dist/at-include --check`) verifies `AGENTS.md` is
up to date without writing anything; CI and the pre-commit hook both rely on
this.
