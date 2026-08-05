# at-include — agent instructions

`at-include` is a Go port of `scripts/build-agents.mjs` from the `app-variants`
project. It flattens `@<path>` Markdown imports the way Claude Code inlines
`@`-referenced files from a `CLAUDE.md`.

**Happy-path output compatibility with the original JS is what matters** —
the generated file's content (banner, `Contents of X (...)` markers, blank-line
placement, single trailing newline, inline-once/cycle behavior) must not
change. Internal implementation is ordinary, idiomatic Go; the JS is a
historical reference for behavior, not an ongoing constraint on how the Go
code is written. Deliberate, user-visible divergences from the JS exist and
are documented inline as comments at the point of divergence (for example,
the generalized banner wording) — read the comment before assuming a
difference is a bug.

@docs/architecture.md

## Conventions this codebase actually follows

See [`docs/conventions.md`](docs/conventions.md) for testing and porting
conventions (differential testing, `t.Parallel()` usage, fixture-case format,
and lint suppression style).

## Build, test, lint

```sh
mise install       # pinned Go toolchain + tools (see mise.toml)
mise run build      # build ./dist/at-include (also injects the version string)
mise run test       # unit tests + the black-box fixture suite under test/
mise run lint       # golangci-lint run
mise run check      # what CI runs: gofumpt -l check, lint, then test
```

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
