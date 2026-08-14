# Changelog

All notable changes to this project will be documented in this file.


## v0.5.0 - 2026-08-13
### Changed
* Breaking: the CLI is now subcommand-based. Bare 'at-include' is a usage error; use 'at-include build' (the old default invocation), 'at-include check' (was --check), 'at-include supplement' (was --hook-mode), and 'at-include imports' (was --list-imports). Update mise tasks, git hooks, CI steps, and agent lifecycle hooks accordingly.

## v0.4.0 - 2026-08-11
### Added
* Changelog entries are now curated per change with Changie: each change ships a fragment under .changes/unreleased/, and releases batch those into CHANGELOG.md and the GitHub Release notes.

## v0.3.0 - 2026-08-06
### None
* Baseline entry marking the start of Changie-based changelog tracking. Changes prior to this version were not tracked with individual changelog fragments.
