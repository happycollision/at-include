# at-include — Design

Date: 2026-08-04
Status: Approved (decisions made autonomously per user instruction)

## Purpose

`at-include` is a single static binary that flattens `@path` imports in Markdown —
reproducing the rules Claude Code uses when it reads a `CLAUDE.md` and inlines
`@`-referenced files. It is a faithful Go port of
`app-variants/scripts/build-agents.mjs`, generalized from that repo's hardcoded
`AGENTS.src.md` → `AGENTS.md` into a reusable CLI other repos can drop into CI or
a git hook.

Two modes:

- **write** (default): flatten the source and write the output file.
- **`--check`**: regenerate in memory, compare to the output file on disk, exit
  nonzero with a diff excerpt when stale. This is the CI/git-hook mode.

## Behavior to preserve exactly

The port must reproduce the JS semantics, quirk for quirk. These are the
non-obvious ones, each of which is pinned by a test:

1. **Token scan.** An `@` anywhere in a line starts a candidate token that runs
   until whitespace. `foo@bar.com` yields the candidate `bar.com`. Trailing
   punctuation is part of the token (`@a/b.md,` → `a/b.md,`).
2. **Resolution filters.** A candidate is inlined only if it resolves to an
   existing *regular file* (relative to the importing file's directory, or used
   as-is when absolute). Anything else is left as literal text — never an error.
   This is what makes rule 1 safe: emails and `@scope/pkg` mentions don't resolve.
3. **Inline code spans are verbatim.** Backtick runs of any length are matched by
   an equal-length closer. Content inside a span is copied through unexpanded. An
   unterminated run emits its backticks literally and scanning continues.
4. **Fenced blocks are verbatim.** ` ``` ` or `~~~`, indent-tolerant. The closer
   must use the same character and be at least as long as the opener, so a
   longer outer fence is not closed by a shorter inner one. A fence line of the
   *other* character inside an open fence is swallowed as content. Fence
   delimiter lines themselves never contain imports.
5. **Inline-once, globally.** A set of already-inlined absolute paths means each
   file's content appears at most once. Repeat and cycle back-edges emit
   `Contents of <rootRel> (already inlined above):` instead — this is what makes
   cycles terminate.
6. **Marker text.** First inline emits
   `Contents of <rootRel> (project instructions, checked into the codebase):`
   followed by a blank line and the expanded content with a guaranteed trailing
   newline. `<rootRel>` is the path relative to the root dir, with `\` → `/`.
7. **`--max-depth n`** counts hops on the ancestor stack and errors when
   `len(stack) > n`, checked on entering a file. `src(0) → a(1) → b(2)` is
   allowed at `n=2`; a third hop is not.
8. **Output assembly.** Banner + `\n\n` + flattened content, then the trailing
   newline run collapsed to exactly one — applied to the *combined* text so the
   invariant holds for empty and all-newline sources.

### Deliberate generalizations

The JS derives root, source, and output from its own location in `scripts/`. A
reusable CLI cannot. So:

- `--src` (default `AGENTS.src.md`), `--out` (default `AGENTS.md`), `--root`
  (default: the directory containing `--src`) are flags. Relative paths resolve
  against the process CWD.
- The banner is templated over the actual src/out names and the `at-include`
  command line rather than hardcoding `node scripts/build-agents.mjs`. It keeps
  the same shape: `> [!IMPORTANT]`, "This file is generated", a link to the
  source, the regenerate command, and the "If you are an agent" rule.
- `--marker-desc` overrides the parenthetical in rule 6 for repos that want
  different wording. Default is the JS string verbatim.

`findImports` — an exported JS helper with no CLI surface — becomes an internal
function plus a hidden-ish `--list-imports` subcommand so the token-scanning
rules stay testable from outside the binary.

## Architecture

```
cmd/at-include/main.go     thin: os.Args → run() → os.Exit(code)
internal/cli/              flag parsing, usage text, exit codes, stdout/stderr wiring
internal/flatten/          the port: scanner, expander, banner, check/diff
  scan.go                  fence + code-span state machine, FindImports
  expand.go                Expander: recursive inline, inlined-set, depth cap
  banner.go                banner template
  check.go                 Check + firstDiffExcerpt
test/                      language-agnostic CLI tests (see below)
```

`internal/cli` takes an explicit `io.Writer` pair and returns an int, so exit
codes and output are testable without a process. `internal/flatten` does the
work behind a small surface: `Flatten(opts) (content string, inlined int, err
error)`, `Generate`, `Check`, `FindImports`.

Exit codes, matching the JS: `0` success, `1` stale-or-runtime-error, `2` usage
error.

## Testing

**The tests are language-agnostic and exercise the built binary in fixture
directories** — per the requirement. Two layers:

1. **Fixture-driven golden tests (the main suite).** Each case is a directory
   under `test/cases/<name>/`:

   ```
   test/cases/diamond-import/
     cmd            argv, one per line (e.g. "--check")
     files/         the tree to copy into a temp dir
     expect/
       exit         expected exit code
       stdout       expected stdout (optional; substring-matched via `~` prefix)
       stderr       expected stderr (optional)
       AGENTS.md    expected output file content, byte for byte
   ```

   A tiny runner copies `files/` to a temp dir, runs the binary there, and
   compares. Adding a case is adding a directory — no code. This is the harness
   the port's behavior is pinned by, and it covers every quirk above plus every
   test in `build-agents.test.mjs`.

2. **Go unit tests** alongside `internal/flatten` for the state machine's edge
   cases, where a table test is clearer than a fixture directory.

**Harness choice.** I evaluated `bats` (bash, well-known but a dependency and
awkward on Windows), `testscript` (`rogpeppe/go-internal` — excellent, but its
`.txtar` scripts are a Go-ecosystem idiom), and a plain fixture-directory runner.
I chose the fixture-directory runner driven by Go's `testing` package: the
*cases* are pure data readable and writable by anyone, Go is used only to shell
out to the binary (as the user specified), and it needs no extra runtime. The
runner is ~150 lines.

## Tooling

- **mise** owns everything, with `mise.lock` committed: `go`, `golangci-lint`,
  `gofumpt`, `goreleaser`, `lefthook`. Tasks in `mise.toml`: `build`, `test`,
  `lint`, `fmt`, `check`, `ci`.
- **golangci-lint** with a curated set beyond the defaults (`revive`,
  `gosec`, `errcheck`, `govet`, `staticcheck`, `misspell`, `unparam`,
  `errorlint`, `gocritic`), plus a relaxed profile for `test/`.
- **gofumpt** for formatting (stricter superset of gofmt).
- **goreleaser** for cross-compiled archives: linux and darwin on amd64+arm64,
  and windows on amd64 — Windows is nearly free here since the port has no
  syscall-level dependencies. Path separators are already normalized (rule 6).
- **GitHub Actions**: a `ci` workflow (lint + test on ubuntu and macos) and a
  `release` workflow on tags running goreleaser.
- **lefthook** for this repo's own hooks, which doubles as the worked example the
  README points at for consumers.

## README emphasis

Lead with the mise install (`mise use -g ubi:...` / a `[tools]` entry pinned in
the consumer's `mise.toml`), then `at-include --check` as a CI step, then a
lefthook `pre-commit` example. Explain the `@path` rules in a short table so
consumers know what will and won't be inlined.

## Out of scope

Watch mode, config files, glob imports, frontmatter handling, `@` imports from
URLs, and any transformation of the inlined content beyond the marker.
