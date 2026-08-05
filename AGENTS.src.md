# at-include — agent instructions

`at-include` is a Go port of `scripts/build-agents.mjs` from the `app-variants`
project. It flattens `@<path>` Markdown imports the way Claude Code inlines
`@`-referenced files from a `CLAUDE.md`.

**Exact JS behavioral fidelity is the top priority of this codebase** — more
important than Go idiom. When the two pull in different directions, match the
JS. Deliberate divergences from the JS exist and are documented inline as
comments at the point of divergence (for example, the generalized banner
wording, or accepted differences in invalid-UTF-8 handling) — read the comment
before assuming a difference is a bug.

## Architecture

```
cmd/at-include        thin main: argv/env in, cli.Run's exit code out
internal/cli          flag parsing, exit codes, usage text (cli.go)
internal/flatten       the actual port of build-agents.mjs
  scan.go              fence/inline-code state machine + FindImports
                        (used by --list-imports, not by expansion itself)
  expand.go            the recursive expander (Flatten/Options/expander)
  banner.go            the generated-file banner + output assembly
  check.go             --check: regenerate in memory, diff against disk
test/                  black-box fixture suite driving the built binary
```

`cmd/at-include/main.go` should stay a thin wrapper. Argument parsing, exit
codes, and all user-facing text belong in `internal/cli`. All import-flattening
logic belongs in `internal/flatten` and should have no knowledge of flags or
exit codes.

Within `internal/flatten`, note the deliberate asymmetry between `scan.go`'s
`FindImports` (strips inline code spans before scanning for `@tokens`,
matching JS `findImports`) and `expand.go`'s `transformLine` (does NOT strip
code spans first — it scans the raw line, so a `` ` `` inside an `@token`
during expansion is just a non-whitespace character). The two functions are
allowed to disagree on the same input; this is intentional and matches the JS,
which has the same asymmetry between its own `findImports` and
`transformLine`. Don't "fix" this by unifying the two scanners.

## Conventions this codebase actually follows

The conventions below are verified against the code, not assumed — see the
cited files if you want to check them yourself.

@docs/conventions.md

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
file such as `docs/conventions.md`) and regenerate:

```sh
mise run build && ./dist/at-include
```

`mise run docs:check` (or `./dist/at-include --check`) verifies `AGENTS.md` is
up to date without writing anything; CI and the pre-commit hook both rely on
this.
