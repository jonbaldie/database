# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.10] - 2026-09-09

### Added

- Accepted the remaining MySQL cast destinations in `CAST` and `CONVERT`: `BINARY` with its optional length, `DOUBLE`, `REAL`, `FLOAT`, `DATE`, `DATETIME` and `TIME` with their optional fractional-second precision, and `YEAR`. A temporal or approximate-numeric result advertises the target family's wire metadata, a temporal source contributes its date or clock part to a narrower target, and a malformed or out-of-range value still fails with the family's MySQL error identity.

### Fixed

- Dropped a table's durable rows and indexes with `DROP TABLE` instead of keeping them until a later `CREATE TABLE` of the same name adopted them.
- Accepted `LIKE 'pattern' ESCAPE ''` as disabling the escape character instead of failing with error 1064.
- Returned the BIGINT result for `DIV` by a floating-point value when the truncated quotient equals `MaxInt64`, instead of a false overflow error 1690.
- Prevented the overflow guard for `MinInt64 * -1` from trapping on x86_64; the expression now returns `MinInt64` as MySQL does.
- Persisted `ALTER TABLE` changes that modify only a column type or nullability so they survive a graceful restart.
- Reported MySQL error 1452 for foreign key violations on new or changed child rows in self-referencing tables, including orphan `INSERT` values, instead of MySQL error 1451, which stays reserved for deleted or updated parent rows.
- Rejected `DROP DATABASE` with MySQL error 3730 when a table in the database is referenced by a foreign key constraint in another database, instead of dropping it and leaving the child table's constraint pointing at an unknown database.
- Renamed the column references held by `PRIMARY KEY`, `UNIQUE`, and `FOREIGN KEY` constraints when `ALTER TABLE ... RENAME COLUMN` or `CHANGE COLUMN` renames a column, instead of failing with MySQL error 1072 or publishing constraints that name a column that no longer exists.
- Negated unsigned values in the signed `BIGINT` domain: a value at or below `MaxInt64 + 1` negates to a signed result, and only a value above that fails with MySQL error 1690, instead of rejecting every unary minus on an unsigned value.

## [0.2.9] - 2026-09-08

### Fixed

- Returned a DECIMAL result for `DIV` when the truncated quotient exceeds the signed 64-bit range, instead of MySQL error 1690.
- Resolved SELECT aliases that appear inside larger `ORDER BY` expressions.
- Treated negative integer literals in `ORDER BY` and `GROUP BY` as constant expressions rather than column ordinals.
- Rounded `DOUBLE` values half away from zero.
- Evaluated indexed column comparisons in cross-join `WHERE` clauses against columns from other tables.
- Allowed non-aggregated columns that are functionally dependent on a grouped primary key or unique NOT NULL key.
- Stripped `--`, `#`, and in-statement `/* */` comments from SQL before dispatch.
- Clamped empty window `ROWS` frames so frame bounds do not move backwards.
- Rejected unsupported `FULL JOIN` and `NATURAL JOIN` instead of parsing them as table aliases.
- Preserved `WHERE` clauses in `EXISTS` subqueries that omit `FROM`.
- Enforced grant authorization for cross-database operations.
- Rejected `TRUNCATE TABLE` on parent tables referenced by foreign keys with MySQL error 1701.

## [0.2.8] - 2026-09-07

### Fixed

- Recognized `UNIQUE` table constraints without whitespace before opening parentheses in `CREATE TABLE` and `ALTER TABLE` statements.
- Returned an empty result set for queries combining `ORDER BY` with `LIMIT 0` while preserving column metadata.

## [0.2.7] - 2026-09-04

### Added

- Added root `Dockerfile` exposing MySQL (3306) and diagnostics (8080) ports for general usage.
- Added `dev.Dockerfile` configured with development toolchain and utilities.

### Fixed

- Forwarded bare `data validate`, `data inspect`, and `upgrade` CLI commands directly to command handlers, reporting `invalid_input` (exit code 2) when required inputs are missing.
- Classified concurrent `database serve` on an already-serving data directory as `precondition` (exit code 3).
- Classified `database restore` targeting a non-empty directory as `precondition` (exit code 3).
- Populated required operator facts in terminal `details` for `backup inspect`, `restore`, and `upgrade` JSON results.
- Inspected active directory lock `.running.lock` during `data inspect` to report state as `serving` when the server is actively running.

## [0.2.6] - 2026-09-04

### Fixed

- Checked `chmod`, `write`, `sync`, and `close` errors during instance upgrade
  file creation to prevent metadata corruption.
- Returned an empty string for `SUBSTRING` when a negative start position
  exceeds the string length.
- Returned the 1-based start index for `LOCATE` when the searched needle is an
  empty string.
- Supported `DIV` integer division on unsigned operands exceeding `MaxInt64`
  and preserved the unsigned integer domain.
- Preserved the unsigned integer domain in `%` and `MOD` operations and
  rejected negative remainder results with error 1690.
- Rejected `DROP TABLE` on parent tables referenced by foreign keys with MySQL
  error 3730.
- Encoded `BIT` column results as packed bytes over the MySQL wire protocol.
- Supported `ORDER BY` and `LIMIT` clauses on `DELETE` statements.
- Accepted `CHECK(expr)` constraints without whitespace preceding the opening
  parenthesis.
- Bound `LIKE` wildcard matching to prevent exponential backtracking.
- Formatted `DOUBLE` numeric values without exponential scientific notation
  when within standard display precision.
- Parsed mixed outer join chains combining `LEFT JOIN` and `RIGHT JOIN`.
- Exposed diagnostics through `SHOW WARNINGS`.
- Evaluated `UPDATE` and `DELETE` `WHERE` clauses using relational expressions.
- Compared `BINARY` and `VARBINARY` column values bytewise.
- Evaluated scalar subqueries within comparison and `WHERE` predicates.
- Supported arithmetic expressions on aggregate function projections.
- Reported MySQL error 1364 when non-nullable columns without default values
  are omitted from `INSERT` statements.
- Supported explicit `DEFAULT` values in `INSERT INTO ... VALUES` expressions.
- Evaluated multi-column `UPDATE` assignment expressions sequentially from left
  to right.
- Preserved `NULL` frame bounds in window function range specifications.
- Supported empty value lists in `INSERT INTO table VALUES ()`.
- Enforced out-of-range bounds on unsigned arithmetic expressions.
- Made `LIKE` pattern comparisons accent-insensitive under `utf8mb4_0900_ai_ci`.

## [0.2.5] - 2026-08-28

### Fixed

- Accepted `FOREIGN KEY ... REFERENCES table(column)` when no space precedes
  the column list.
- Evaluated `NOW()`, `CURDATE()`, `VERSION()`, and `DATABASE()` in ordinary
  expressions, not only as a whole-statement `SELECT`.
- Stripped trailing spaces from `CHAR` values on store so retrieval matches
  MySQL CHAR.
- Padded `BINARY(n)` values with `0x00` to the declared width.
- Ran `information_schema` `SELECT` through the relational engine so `WHERE`,
  `ORDER BY`, `GROUP BY`, `LIMIT`, and aliases work.
- Added contracted `information_schema` views: `STATISTICS`,
  `TABLE_CONSTRAINTS`, `KEY_COLUMN_USAGE`, `REFERENTIAL_CONSTRAINTS`,
  `CHECK_CONSTRAINTS`, `CHARACTER_SETS`, `COLLATIONS`, `ACCOUNTS`,
  `ACCOUNT_GRANTS`, and `PROCESSLIST`.
- Accepted `SHOW GRANTS`, `SHOW GRANTS FOR CURRENT_USER`, and
  `SHOW GRANTS FOR 'name'`.
- Matched `LIKE` with the column collation so `utf8mb4_bin` is case-sensitive.
- Returned MySQL error 1267 for mixed `utf8mb4_0900_ai_ci` and `utf8mb4_bin`
  column comparisons.
- Compared `DATE` and `DATETIME` as temporal values so a date equals the same
  calendar day at midnight.

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

[0.2.10]: https://github.com/jonbaldie/database/compare/v0.2.9...v0.2.10
[0.2.9]: https://github.com/jonbaldie/database/compare/v0.2.8...v0.2.9
[0.2.8]: https://github.com/jonbaldie/database/compare/v0.2.7...v0.2.8
[0.2.7]: https://github.com/jonbaldie/database/compare/v0.2.6...v0.2.7
[0.2.6]: https://github.com/jonbaldie/database/compare/v0.2.5...v0.2.6
[0.2.5]: https://github.com/jonbaldie/database/compare/v0.2.4...v0.2.5
[0.2.4]: https://github.com/jonbaldie/database/compare/v0.2.3...v0.2.4
[0.2.3]: https://github.com/jonbaldie/database/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/jonbaldie/database/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/jonbaldie/database/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/jonbaldie/database/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jonbaldie/database/releases/tag/v0.1.0
