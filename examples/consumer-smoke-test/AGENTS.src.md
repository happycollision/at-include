# Consumer smoke test

This directory installs `at-include` from its published release via `mise.toml`,
exactly the way the README tells a consumer to. It exists to catch installation
regressions — that the release archives resolve, the binary runs, and the
`--check` CI gate behaves.

@notes/setup.md

Things that must stay literal: `@notes/setup.md` in a code span,
an email like dev@example.com, and a package such as @scope/pkg.

```
@notes/setup.md
```
