## Testing and porting conventions

- **Fidelity over idiom, as a tiebreaker.** Where the JS spec's exact
  behavior and Go's usual style conflict, fidelity wins, and the divergence
  gets a comment explaining why (see `expand.go`'s depth-exceeded error,
  which capitalizes "Import" and disables `staticcheck`'s ST1005 to match the
  JS spec's exact wording verbatim, or `cli.go`'s "Unknown argument:" message,
  same story).
- **Differential testing against the real JS is the standard for subtle
  ports**, not just a nice-to-have. Several bugs in this port were caught by
  literally running both implementations on the same input and diffing —
  not by reasoning about the spec — most notably around JS's Unicode `\s`
  whitespace semantics (`scan.go`'s `jsIsSpace`) and trailing-newline
  normalization (`banner.go`'s `Assemble`, pinned by
  `check_test.go`'s `TestAssembleMatchesJS`). If you're porting or changing
  behavior that touches the JS spec, write a differential case before trusting
  your own reasoning about an edge case.
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
