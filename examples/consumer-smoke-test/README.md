# Consumer smoke test

This directory pretends to be somebody else's repo. Its `mise.toml` installs
`at-include` from its **published GitHub release** — not from the source a few
directories up — so it exercises the same path a real user follows from the
[main README](../../README.md).

It exists to catch installation regressions that the main test suite cannot
see, because the main suite builds the binary from source: a release archive
that fails to resolve, an asset named differently than mise expects, or version
metadata that gets lost between `goreleaser` and the shipped binary.

## Running it

```bash
mise trust && mise install
mise run docs        # regenerate AGENTS.md
mise run docs:check  # the CI gate — exits 1 when AGENTS.md is stale
```

`mise trust` is needed once: mise refuses to read a config file it has not been
told to trust. Consumers hit this too, on first use in any new repo.

## What it verifies

- The release archive downloads, its checksum verifies, and GitHub attestations
  and SLSA provenance check out.
- `at-include --version` reports `0.1.1` — proving the `-ldflags` version
  injection survives the goreleaser build, not just the local `mise run build`.
- Nested imports resolve relative to the importing file (`notes/setup.md` pulls
  in `notes/shared.md`).
- Inline code spans, fenced blocks, emails, and `@scope/pkg` mentions stay
  literal.
- `--check` exits 0 when fresh and 1 when stale, printing a diff excerpt and a
  regeneration command that actually works.
- The guard that stops `--out` from overwriting `--src` (exit 2, source intact).

Bumping the pinned version here after a release is the cheapest way to confirm
the new release is installable.
