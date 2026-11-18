# Changelog

All notable changes to this project are documented in this file.

## [0.1.2] - 2026-11-18

### Added

- Bug report and feature request issue templates, plus issue-template contact
  links for the documentation and contributing guide. (#4)
- Configurable export destination, filename template, output type, column
  selection, and sort order under `export` in `config.yaml`. Relative paths
  are resolved under the Kubernova configuration directory, and `<cluster>`,
  `<context>`, `<timestamp>`, and `YYMMDD-HHmm:ss` placeholders are expanded.
  (#5)
- `export.markdown_table_padded` option to align Markdown table cells so
  columns line up in source and rendered Markdown. (#6)
- Resource severity levels `OK`, `MEDIUM`, and `HIGH`, configurable through
  `resources.severity`. HIGH requires both a relative and an absolute limit
  difference, with separate CPU and memory minima. OOMKilled containers are
  always HIGH. (#7)
- `P10` SEVERITY column and per-level container counts in JSON and Markdown
  exports. (#7)
- Severity formula and P10 query notes in exported reports. (#7)

### Changed

- `ctrl+e` and `:export` without an explicit type now use the configured
  `export.type` instead of always writing both formats. (#5)
- Export filenames use the `YYMMDD-HHmm:ss` timestamp pattern by default. (#5)
- Documentation and generated defaults describe the new export and severity
  settings. (#5, #6, #7)

### Fixed

- CPU and memory usage columns now show each pod/container's own percentile
  instead of repeating the workload maximum on every replica. Suggestions
  still use the owner-level maximum and remain identical across replicas.
  (#8)
- Pressing `space` on a collapsed replica row now hides or restores the whole
  replica group, instead of revealing and hiding replicas one at a time. (#9)

## [0.1.0] - 2026-11-10

Initial release.
