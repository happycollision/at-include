# Stdin/stdout piping — Design

Date: 2026-08-05
Status: Approved

## Purpose

Let `at-include` be used as a pure text-transform step in a shell pipeline, or
fed ad-hoc/editor-buffer content, without requiring a real `AGENTS.src.md` file
on disk. `--src -` reads the source from stdin; `--out -` (or the new stdin
default described below) writes the flattened result to stdout instead of a
file.

Three motivating uses, all satisfied by the same mechanism:

- Ad-hoc one-liners: `printf '...' | at-include --src -`, without creating a
  temp file just to flatten a scratch string.
- Editor/tool integration: pipe a buffer's current content in to preview what
  it would flatten to, without touching disk.
- Composing with other CLI tools: `some-generator | at-include | some-consumer`,
  treating at-include as a plain stdin→stdout transform in a larger pipeline.

## Behavior

### Triggering stdin/stdout

- `--src -` means "read the source from stdin" instead of a file path. This is
  a sentinel recognized before path resolution — `-` is never treated as a
  literal filename for `--src`.
- `--out -` means "write the flattened result to stdout" instead of a file.
  Same sentinel treatment.
- When `--src -` is given and `--out` is **not** explicitly passed, `--out`
  defaults to stdout rather than `AGENTS.md`. (Symmetric pairing: piping in
  without saying where output goes pipes out.) An explicit `--out <file>` is
  still honored and writes to that file as normal.
- `--src -` and `--out -` compose freely, including `--src - --out -` (read
  stdin, write result to stdout) and `--src - --out <file>` (read stdin, write
  a real output file, banner still suppressed per below).

### `--root` default

- `--root` normally defaults to `filepath.Dir(--src)`. With `--src -` there is
  no source file to take a directory from, so `--root` defaults to the process
  current working directory instead. An explicit `--root` flag still overrides
  this, exactly as today.
- This CWD default is what relative `@path` tokens found inside the piped
  content resolve against — e.g. piped content containing `@notes.md` resolves
  `notes.md` relative to CWD (or `--root` if given). Imports found via
  recursion always come from disk, never from stdin; only the top-level source
  text itself can come from stdin.

### Same-path collision check

- Today, `--out` must not resolve to the same file as `--src` (protects
  hand-authored source from being overwritten). This check is skipped entirely
  when `--src` is stdin — stdin is a stream, not a path, so the check is
  structurally inapplicable. `--src - --out -` is explicitly allowed and
  harmless (reads stdin, writes to stdout — two independent streams).

### Banner

- The generated-file banner (`> [!IMPORTANT] ... This file is generated ...`)
  is skipped whenever `--src` is stdin, regardless of what `--out` is —
  including `--src - --out <realfile>`. There is no real source filename to
  point at, so the banner would either be misleading or require inventing
  placeholder text; simplest correct behavior is to omit it.
- The banner still prints normally in every other case, including
  `--src <realfile> --out -` (real source file, output piped to stdout) — the
  banner's applicability depends only on whether `--src` is stdin, not on
  `--out`.
- No opt-in flag to force the banner on for stdin sources. Out of scope for
  this feature; can be revisited later if requested.

### `--check`

- Rejected as a usage error when combined with `--src -`. `--check`'s entire
  purpose is "did the source file change without the output being
  regenerated" — a comparison that requires a stable, re-readable source file.
  Piped stdin content is transient and can't be the basis of that comparison.
  Error message should make this reasoning clear, not just "invalid
  combination."

### `--list-imports`

- Works unchanged with `--src -`: reads stdin instead of a file, then scans
  the text for `@token` candidates the same way it does for a file today. No
  special-casing needed since this path never touches `--out` or the banner.

### Empty stdin

- Empty piped input (e.g. `printf '' | at-include --src -`) is treated as
  empty source content, no error — consistent with flattening an empty file
  today. No TTY detection or "did you forget to pipe something" heuristics.

## Architecture

`internal/flatten.Options` gains a way to say "the source is stdin" instead of
a real file at `SrcPath`. The cleanest fit with the existing shape: an
`io.Reader` field (e.g. `Stdin io.Reader`) that `expandFile`'s top-level call
reads from instead of `os.ReadFile` — but **only** for the initial call
(`depth == 0`); every recursive `@path` expansion still reads from disk via
`os.ReadFile` exactly as it does today, using `RootDir`/the importing file's
directory as it always has. `SrcPath` itself is not repurposed to hold a
sentinel value like `"-"` — resolving to an OS path named `-` (a legal, if
unusual, filename) must stay unambiguous from the stdin sentinel that `--src -`
means at the CLI layer.

`internal/cli.Run` already threads `stdout, stderr io.Writer` through as
parameters instead of touching `os.Stdout`/`os.Stderr` directly; it gains a
matching `stdin io.Reader` parameter for the same reason — testability without
touching real process streams. `cmd/at-include/main.go` passes `os.Stdin` at
the real entry point.

`resolveOptions` (or a new step alongside it) detects the `-` sentinel on
`--src`/`--out` before any `filepath.Abs`/`filepath.Dir` calls, since those
would otherwise treat `-` as a literal relative filename. This detection
drives: the `--root` CWD-default branch, skipping the same-path collision
check, and skipping the banner — all three keyed off "is `--src` stdin," not
off string-matching `-` in multiple places.

`runGenerate` writes to `fOpts` output as before, but the output writer
becomes `stdout` (the CLI's own stdout parameter) instead of `os.WriteFile`
when `--out` is stdout. Stdout must contain only the flattened content in this
mode, nothing else mixed in — so the normal success message ("Generated ...
from ... (N files inlined).") is suppressed entirely when `--out` is stdout,
rather than moved to stderr. Quiet-on-success stdout piping (no status message
on either stream) matches standard Unix filter conventions. This suppression
is keyed off "is `--out` stdout," independent of whether `--src` is stdin —
e.g. `--src <realfile> --out -` also suppresses the message.

## Non-goals

- No auto-detection of piped stdin without `--src -` (e.g. via TTY checks).
  Explicit sentinel only, consistent with `tar`/`jq`/`cat -`-style tools.
- No `--check` support for stdin sources.
- No opt-in banner override for stdin sources.
