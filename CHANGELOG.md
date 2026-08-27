# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.4] - 2026-08-27

### Changed

- Used hash joins for supported equality predicates. Work now grows with input
  rows and output rows instead of every left and right row pair.
- Used indexed parent keys, direct lock-resource lookups, staged insert key
  maps, and one-pass predicate parsing. These changes remove quadratic scans
  from normal validation, locking, batch insert, and parsing paths.
- Kept ordered indexes between queries and used index bounds. Full scans no
  longer sort all rows for each query, and bounded scans can start and stop at
  the range limits.
- Evaluated supported window functions with linear partition passes after
  sorting.
- Applied durable point updates as deltas instead of full-table copies. Row-only
  writes no longer rebuild unchanged catalog metadata and indexes.
- Streamed backup and restore data with bounded buffers instead of reading
  complete data sets into memory.
- Added atomic checkpoints and safe WAL prefix removal. Startup now replays only
  changes after the latest valid checkpoint.

## [0.2.3] - 2026-08-26

### Fixed

- Rebuilt the primary-key index after same-length rewrites so `REPLACE` then
  point `UPDATE` changes the replaced row.
- Calculated `INSERT` and `UPDATE` assignment expressions instead of storing
  the source text.
- Rejected character-to-numeric predicates without an explicit cast.
- Applied the 64-scalar identifier limit to `SAVEPOINT` and CTE names.
- Accepted extra spaces in supported transaction statements.
- Added `LIKE` for scalar expressions, `WHERE`, `SHOW TABLES LIKE`, and
  `SHOW DATABASES LIKE`.

## [0.2.2] - 2026-08-20

### Fixed

- Preserved control bytes in durable row values during WAL replay by using
  length-prefixed WAL fields.
- Validated transaction changes before WAL publication so rejected duplicate
  writes do not corrupt state after a restart.
- Replayed primary-key updates using the old key and corrected unique-index
  handling for nullable, composite, and no-primary-key tables.

## [0.2.1] - 2026-08-20

### Fixed

- Bounded scalar-expression parser recursion (nested parentheses, `NOT`
  chains, and unary `+`/`-` runs) to stop an unrecoverable stack-overflow
  crash from a single query, reachable within the default `max_allowed_packet`
  size. Excessive nesting now fails with the MySQL
  `ER_STACK_OVERRUN_NEED_MORE` (1436) error identity instead of crashing the
  server.

## [0.2.0] - 2026-08-13

### Added

- MySQL wire tests for text and prepared statements, settings, accounts,
  transactions, locks, cancellation, restart durability, and Query explanation.
- Required vulnerability checks and a required Go Report Card A+ check.

### Changed

- Replaced the first-page project description with a developer trial that uses
  a supported Go client, a prepared query, and Query explanation.
- Routed text, prepared, settings, account, and Query explanation work through
  one normalized statement policy. Removed the old statement execution paths.
- Updated the Go toolchain and dependencies to remove known reachable Go
  standard library vulnerabilities.
- Clarified the Query explanation documentation checks and the classification
  of conformance evidence.

### Notes

- v0.2.0 is experimental. It does not claim production readiness or complete
  MySQL compatibility beyond the documented contracts.
- Data and backup compatibility remain `0.1.x`. The MySQL application
  compatibility profile remains `0.1`. This release does not require a data
  upgrade.

## [0.1.0] - 2026-08-03

### Added

- First public release of the experimental single-node database server in Go.
- MySQL 8.4 classic-protocol application profile with `caching_sha2_password`
  authentication, text and prepared execution, and explicit failure for
  unsupported protocol features.
- Documented MySQL 8.4.11 SQL subset for namespaces, tables, B-tree indexes,
  constraints, CRUD, joins, subqueries, CTEs, set operations, aggregates,
  windows, transactions, catalog metadata, and account administration.
- Versioned query explanation contract (JSON format `1` and stable tabular
  projection), including plan-only, analyzed, and live snapshot modes.
- Operator command family: `init`, `serve`, `shutdown`, `backup`, `restore`,
  `upgrade`, `config validate`, `data validate`, `data inspect`, and `version`.
- Closed server configuration and session settings registries with bounded
  defaults.
- Diagnostics listener for `/live`, `/ready`, and `/metrics`, plus structured
  lifecycle events.
- Native release artifacts for `darwin/arm64`, `linux/amd64`, and `linux/arm64`,
  plus a multi-architecture OCI image index.
- Public conformance, compatibility, distribution, and performance acceptance
  evidence under `docs/`.

### Notes

- v0.1.0 is experimental. It does not claim production readiness or complete
  MySQL compatibility beyond the documented contracts.
- Parent delivery map: https://github.com/jonbaldie/database/issues/1

[0.2.4]: https://github.com/jonbaldie/database/compare/v0.2.3...v0.2.4
[0.2.3]: https://github.com/jonbaldie/database/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/jonbaldie/database/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/jonbaldie/database/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/jonbaldie/database/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jonbaldie/database/releases/tag/v0.1.0
