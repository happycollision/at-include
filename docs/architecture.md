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
expander share one token-scanning rule — see `scanLine`'s doc comment for the
exact statement. In outline: a `@` starts a token only at line start or
immediately after whitespace; the token then runs to the next whitespace
character, except that a backslash-space pair (`\ `) is an escaped space that
continues it; and everything from the first `#` onward is a fragment that is
dropped from the resolved path. Fenced code blocks are skipped entirely by
both, and inline code spans are left verbatim during expansion, so
`--list-imports` always reports exactly the tokens the expander would consider.

These rules were modeled on Claude Code's own import scanner
(`/(?:^|\s)@((?:[^\s\\]|\\ )+)/g`, then truncate at the first `#`, then
`replaceAll("\\ ", " ")`), which was read out of the shipped CLI bundle and
confirmed by observing real imports. That pattern is an undocumented
implementation detail of a specific version, not a published contract: it can
change without notice, and this file records what was true when it was checked
(Claude Code 2.1.221, 2026-08-05). The aim is matching intent and practical
behavior, not byte-level parity — when the two diverge, `at-include`'s own
tests define what this tool does. Consequences worth knowing:

- An email is never scanned: the `@` in `foo@bar.com` follows a word
  character, so no candidate is produced even when a file named `bar.com`
  exists. The same boundary rule means a backtick-adjacent `` `@notes.md ``
  is literal text.
- Escaping is the only way to write a `@path` containing a space. Quoting is
  not a mechanism (`@"a b.md"` yields the candidate `"a`), and there is no
  longest-match-on-disk probing.
- Truncation runs before unescaping, which is observable: `@a\ b#c\ d.md`
  resolves `a b`.

Because `linePiece.Text` is a *resolution candidate* (unescaped, fragment
stripped) rather than source text, `linePiece.Raw` carries the original bytes
after the `@` so the expander can write an unresolvable token back verbatim —
without it, a `@nope.md#frag` that doesn't resolve would be silently rewritten
as `@nope.md`, editing prose that was never an import.

### Known differences from Claude Code

Parity is not a goal in itself, and this list is not exhaustive — it is what is
known as of the last check. Undocumented upstream behavior can change, so expect
this to drift; re-verify before relying on any of it.

- **Candidate acceptance.** After truncating and unescaping, Claude Code filters
  candidates (rejecting a leading `@` or `[#%^&*()]`, otherwise requiring a
  leading `[a-zA-Z0-9._-]` unless the path starts with `./`, `~/`, or `/`).
  `at-include` has no such layer: those candidates just fail to resolve, giving
  the same observable outcome — literal passthrough — without a second notion of
  validity. A path that upstream rejects but that *does* exist on disk would
  differ.
- **Scan surface.** Claude Code runs its pattern over parsed Markdown token
  nodes (skipping `code`/`codespan`, and scanning HTML comments' non-comment
  remainder). `at-include` uses its own line-oriented fence and inline-code
  state machine. These agree on ordinary documents, but exotic Markdown — odd
  HTML blocks, unusual nesting — is not guaranteed to partition identically.
- **Scope.** Claude Code applies limits and features that are irrelevant here or
  deliberately different: a max import depth (`at-include` exposes
  `--max-depth`), file-size caps, `claudeMdExcludes`, symlink policy, and the
  discovery of user/project/managed memory files. `at-include` flattens exactly
  the one source file it is pointed at.

If exact agreement with a particular Claude Code version matters for your
project, pin that version and verify the output rather than assuming.
