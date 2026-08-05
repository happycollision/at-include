## Testing and porting conventions

- **Happy-path output compatibility over idiom, as a tiebreaker — but only
  for what the end user can observe.** The generated file's content (banner,
  `Contents of X (...)` markers, blank-line placement, trailing newline,
  inline-once/cycle behavior) must keep matching what the original JS
  produces; that's pinned by the fixture suite under `test/cases/`. Internal
  implementation details the user never sees — error-string capitalization,
  how a value is parsed, which whitespace predicate is used — should just be
  idiomatic Go. Don't add JS-mimicry machinery (a hand-rolled `Number()`
  parser, a bespoke Unicode whitespace table, a copied ancestor-stack slice)
  to reproduce an internal quirk nobody outside the code can observe.
- **Differential testing against the real JS remains useful for pinning
  user-visible output**, not for validating internal mechanics. Several bugs
  in this port were caught by literally running both implementations on the
  same input and diffing — not by reasoning about the spec — most notably
  around trailing-newline normalization (`banner.go`'s `Assemble`, pinned by
  `check_test.go`'s `TestAssembleMatchesJS`). If you're changing behavior that
  affects generated output, write a differential case (or a fixture under
  `test/cases/`) before trusting your own reasoning about an edge case. The
  JS is a historical reference for what "happy path" means, not a spec that
  every internal code path must trace.
- **Inline `#nosec` / `//nolint` with a justification, not blanket
  exclusions.** `.golangci.yml` has exactly one blanket exclusion: gosec (plus
  errcheck/unparam) is disabled for `_test.go` files and `test/`, because test
  code routinely creates scratch files/dirs with fixed permissions and no
  attacker-controlled input. Everywhere else — `os.ReadFile`/`os.Stat` on
  `@import` targets, a `0o644` output file — the suppression is inline, next
  to the specific line, explaining why it's safe there.
- **`t.Parallel()` in tests, with one documented exception.** Every test file
  under `internal/flatten` and `test/` calls `t.Parallel()`. The exception is
  `internal/cli`: its tests exercise `os.Chdir`, which mutates process-global
  state, so parallel subtests would race on the working directory. This is
  called out explicitly in a package comment at the top of
  `internal/cli/cli_test.go` — read it before adding a new test there.
- **`test/cases/` entries are data, not code.** Adding a fixture case means
  adding a directory with a `cmd` file, an input `files/` tree, and
  `expect/*` files — never writing Go. See `test/README.md` for the exact
  format and a worked example of adding one.
- **Exact-output assertions beat `strings.Contains`.** Fixture expectations
  and unit test assertions compare full expected output (byte-for-byte for
  `expect/files/*`, exact-or-declared-substring for `expect/stdout` /
  `expect/stderr` — see the `~`-prefix substring rule in `test/README.md`)
  rather than checking that an output merely contains some substring. Two
  real bugs during development were masked by a `strings.Contains`-style
  assertion that happened to pass on subtly wrong output; prefer pinning the
  whole expected string (or byte-for-byte file) so a regression can't hide
  behind a partial match.
