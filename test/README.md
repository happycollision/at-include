# at-include fixture test suite

This suite drives the built `at-include` binary as a black box. **Cases are
data, not code** — adding a test case means creating a directory under
`cases/`, not writing Go. `cli_test.go` is the only Go file here; it is a
generic runner that discovers and executes every case directory.

## Running

```sh
go test ./test/ -v
# or, via the project task:
mise run test
```

`TestMain` builds a fresh `at-include` binary into a temp dir once per run
(via plain `go build`, not `mise run build` — see "Why not `mise run build`"
below) and every case subtest runs against that same binary.

## Case directory format

Each directory under `test/cases/<name>/` is one test case:

| Path             | Meaning                                                                                             | Required |
|------------------|------------------------------------------------------------------------------------------------------|----------|
| `cmd`            | Argv for the binary, one token per line. Blank lines and `#`-comment lines are ignored.               | No — absent means "no arguments" |
| `stdin`          | Content piped to the binary's stdin.                                                              | No — absent means no stdin piped (nil, same as an unredirected empty read) |
| `files/`         | A tree copied into a fresh temp dir; the binary runs with this temp dir as its working directory.     | No — absent means "empty working directory" |
| `expect/exit`    | Expected process exit code, as a bare integer.                                                        | No — absent means `0` |
| `expect/stdout`  | Expected stdout. See "Trailing newline policy" and "Substring matching" below.                        | No — absent means "don't check stdout" |
| `expect/stderr`  | Expected stderr. Same rules as `expect/stdout`.                                                       | No — absent means "don't check stderr" |
| `expect/files/`  | A tree of files whose contents are compared **byte for byte** against the same relative paths in the working directory after the run. | No — absent means "don't check any file contents" |
| `expect/absent`  | Paths (one per line, relative to the working directory) that must **not** exist after the run.        | No — absent means "no absence checks" |

A case with none of the `expect/*` files present is legal but nearly useless
(it only checks the process didn't panic/crash) — always assert at least an
exit code or a stream.

### `cmd` format details

- One argv token per line. A token that itself contains a space (e.g. a
  `--marker-desc` value with words in it) still goes on a single line — the
  line *is* the token, only leading/trailing whitespace on that line is
  trimmed, internal spaces are preserved.
- Blank lines and lines whose first non-whitespace character is `#` are
  skipped, so you can comment a `cmd` file to explain a nonobvious flag
  combination.
- A trailing blank line at end of file (which every editor adds) is safely
  ignored — it does not produce a spurious empty final argument.
- There is currently no way to pass a genuinely empty-string argument through
  `cmd` (a blank line is treated as "no token," not as `""`). No existing case
  needs one; if a future case does, extend the format rather than working
  around it.

### Substring matching (the `~` rule)

If `expect/stdout` or `expect/stderr`'s **first line is exactly `~`**, the
rest of the file switches to substring mode: every remaining non-blank line
must appear *somewhere* in the actual output (as a substring), in any
position, rather than the whole stream matching exactly. This is for
messages where matching byte-for-byte would be brittle against non-essential
detail, most importantly:

- Full `--help`/usage text (long, and the exact banner wording isn't the
  point of the case).
- Error messages that embed a full absolute temp-dir path (e.g.
  `missing-source`'s `no such file or directory` message) — the interesting,
  stable part is the `at-include:` prefix and the mentioned filename, not the
  ever-changing absolute path.

Without the leading `~` line, the comparison is exact (module the
trailing-newline handling below).

### Trailing-newline policy

Comparison for `expect/stdout` and `expect/stderr` strips **at most one**
trailing newline (and a preceding `\r`, if present) from both the expectation
file and the actual captured output before comparing — see
`trimOneTrailingNewline` in `cli_test.go`. This is a deliberate middle ground:

- A **byte-exact** comparison would force every `expect/stdout` fixture file
  to be saved with no trailing newline, which nearly every editor fights
  you on, and would make an intentionally-empty expectation file
  indistinguishable from "don't care" (an empty file is exactly the sentinel
  `os.ReadFile` treats as "file is absent" only when the file doesn't exist
  at all — a present-but-empty file is a real, meaningful "expect empty
  output" case and must keep working).
- A **fully-normalized** comparison (stripping *all* trailing newlines, e.g.
  via `strings.TrimRight(s, "\n")`) would silently hide a bug where the
  program emits extra trailing blank lines it shouldn't.

Stripping exactly one trailing newline from each side gets both properties:
normal fixture files (which end in the one newline any editor appends) compare
correctly against real one-newline-terminated program output, a 0-byte
expectation file correctly matches genuinely empty output, and a program
regression that adds or drops that final newline is still caught (because
only one `\n` is ever removed per side, not an unbounded run).

This same policy is documented at `trimOneTrailingNewline`'s doc comment in
`cli_test.go`; keep the two in sync if either changes.

### File content comparison: byte-exact, no CRLF normalization

`expect/files/*` are compared **byte for byte** against the corresponding
path in the working directory — no CRLF-vs-LF normalization is applied.

This is deliberate, not an oversight. The repository's `.gitattributes` marks
`test/cases/** -text`, which tells Git to check these fixture files out with
their exact recorded bytes on every platform (no line-ending translation).
Given that guarantee already holds, a byte-exact comparison is strictly more
useful than a normalizing one: normalizing away `\r\n`↔`\n` differences would
also hide a genuine bug where the tool corrupts line endings on the way
through (e.g. silently converting CRLF input to LF). If some future case
legitimately needs to store CRLF content on purpose, it still can — the
`-text` attribute preserves whatever bytes are checked in either way.

### `expect/absent`: asserting a file must NOT exist

Comparing `expect/files/*` only ever checks paths you explicitly listed —
it has no way to notice an *unexpected* file the tool wrote (for example, a
usage error accidentally still leaving behind an output file, or
`imports` writing `AGENTS.md` when it should only print to stdout).

`expect/absent` closes that gap: it lists paths (one per line, relative to
the working directory, blank lines and `#`-comments ignored) that must **not**
exist after the run. See `cases/list-imports/expect/absent` and
`cases/unknown-flag/expect/absent` for examples — both assert `AGENTS.md` is
never written.

## Why not `mise run build`?

The suite builds its own copy of the binary with a plain `go build -o <tmp>
../cmd/at-include` rather than shelling out to `mise run build`. The mise
build task also injects a real version string via
`-ldflags "... -X .../cli.Version=$(git describe ...)"`, which would make the
test suite's behavior depend on the state of git tags in whatever checkout
it's running in (CI clones, shallow clones, and worktrees all commonly lack
full tag history). Since none of the fixture cases care about a *specific*
version number, the suite intentionally forgoes that ldflags injection: a
plain `go build` leaves `cli.Version` at its zero value, so `at-include
--version` prints `at-include dev` in this harness. See `cases/version` for
how that's asserted (a `~`-prefixed substring match against `at-include dev`,
not the real release version).

## Worked example: adding a new case

Say you want to pin the behavior of `build --out /dev/null`-style edge cases
(this is illustrative, not a real gap). Steps:

1. `mkdir -p test/cases/build-out-devnull/files`
2. Add whatever fixture files the case needs under
   `test/cases/build-out-devnull/files/`, e.g. an `AGENTS.src.md`.
3. Write `test/cases/build-out-devnull/cmd` with the argv, one token per line —
   the subcommand name comes first:
   ```
   build
   --out
   /dev/null
   ```
4. Build the real binary (`mise run build`) and run it by hand against a copy
   of the same `files/` tree, exactly as the harness would, to generate the
   real golden output — **never hand-write expected output**:
   ```sh
   cp -R test/cases/build-out-devnull/files /tmp/try && cd /tmp/try
   /path/to/at-include build --out /dev/null
   echo "exit=$?"
   ```
5. Read the actual stdout/stderr/exit code/output files it produced and
   confirm they match the documented behavior (don't just copy a buggy run
   blindly — a golden file is only as trustworthy as the verification behind
   it).
6. Populate `test/cases/build-out-devnull/expect/` from what you observed:
   `exit`, `stdout`, `stderr`, `files/...`, and/or `absent` as needed.
7. Run `go test ./test/ -run TestCases/build-out-devnull -v` to confirm it passes,
   then run the full suite (`go test ./test/ -v`) to make sure nothing else
   broke.

No Go code changes are required for any of the above.
