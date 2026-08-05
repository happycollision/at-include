## Architecture

```
cmd/at-include        thin main: argv/env in, cli.Run's exit code out
internal/cli          flag parsing, exit codes, usage text (cli.go)
internal/flatten       the @path scan/expand/check logic
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

`scan.go`'s `FindImports` (used by `--list-imports`) and `expand.go`'s
expander share one token-scanning rule: an `@token` runs from the `@` to the
next whitespace character, full stop — see `transformLine`'s doc comment for
the exact rule. Fenced code blocks are skipped entirely by both, and inline
code spans are left verbatim during expansion; what a `@token` sitting right
next to a backtick means is governed by the same scan in both places, so
`--list-imports` always reports exactly the tokens the expander would
consider.
