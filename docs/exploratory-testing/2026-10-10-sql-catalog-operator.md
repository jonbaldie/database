# Exploratory testing pass: SQL, catalog, and operator journeys

**Date:** 2026-10-10

**Build:** `database` 0.2.18, commit `1bfca6ca872e66f471c621445a3f7e82c464fa54`, build identity `local`, darwin/arm64, `go1.26.9`

**Surface driven:** the operator CLI and the MySQL classic wire protocol, through a small Go program that uses `github.com/go-sql-driver/mysql` v1.9.3. No `internal/` package was used to obtain a result. Source was read only to explain an already-observed failure.

**Instance:** one disposable data directory from `database init`, served on `127.0.0.1:13306` with diagnostics on `127.0.0.1:18080`. A restored copy and two short non-loopback serves used separate ports and were stopped before cleanup. The local checkout of `main` was 28 commits behind `origin/main`. This pass tested `origin/main`.

**Evidence:** [2026-10-10-sql-catalog-operator/evidence](2026-10-10-sql-catalog-operator/evidence). The run directory under the fleet scratch path was not kept.

This is an exploratory pass, not a conformance run. It attempted three user journeys and followed surprises. It does not claim coverage of the v0.1 contracts.

## Result

Five product defects confirmed and filed:

| # | Issue | Area |
| --- | --- | --- |
| 1 | [#542](https://github.com/jonbaldie/database/issues/542) | `information_schema` ordinal order is text order |
| 2 | [#543](https://github.com/jonbaldie/database/issues/543) | `TABLES` and `COLUMNS` omit contracted columns |
| 3 | [#544](https://github.com/jonbaldie/database/issues/544) | Missing-table schema changes use the wrong error number |
| 4 | [#545](https://github.com/jonbaldie/database/issues/545) | `--result=json` hides the non-loopback TLS warning until shutdown |
| 5 | [#546](https://github.com/jonbaldie/database/issues/546) | `VERSION()` and `EXPLAIN` `server_version` stay at `0.2.0-dev` |

Open issues [#448](https://github.com/jonbaldie/database/issues/448) and [#484](https://github.com/jonbaldie/database/issues/484) still reproduce. They were not filed again.

## Journeys attempted

### A. An application follows the README trial and inspects a query

**Goal:** create the trial table, run the prepared `priority >= ?` query, and read `EXPLAIN ANALYZE FORMAT=JSON`. Then correct a bad insert and continue. Expectation: [`README.md`](../../README.md) and [`docs/query-explanation/README.md`](../query-explanation/README.md).

Ordinary path succeeded. The prepared query returned `2: test query behaviour`. The explanation document had `format_version` 1 and `mode` `analyze`. A non-numeric priority failed with `1366`, and the corrected insert was stored.

Variations attempted: `EXPLAIN FORMAT=JSON` without `ANALYZE`, `EXPLAIN FORMAT=JSON FOR CONNECTION` during a lock wait, a forced index, `LIKE`, `EXISTS`, a scalar subquery, division by zero, `CAST`, a multi-row insert with one bad check, `GROUP BY`, and `SET time_zone`.

This journey produced [#546](https://github.com/jonbaldie/database/issues/546). `BETWEEN` and a value-list `IN` predicate returned `1064` `unsupported expression`. Those forms are not named in the SQL contract, so they are recorded as usability observations, not defects.

### B. An application evolves a schema and reads the catalog

**Goal:** add tables, a check, a foreign key, and indexes, then read the result through `SHOW` and `information_schema`. Correct a failed insert and a failed `ALTER`. Expectation: [`docs/mysql-sql-behaviour.md`](../mysql-sql-behaviour.md) and [`docs/catalog-metadata.md`](../catalog-metadata.md).

Ordinary path succeeded after the check was written as `priority >= 1 AND priority <= 5`. A missing parent failed with `1452`. A failed check failed with `3819` and left the earlier rows in place. A check that would reject existing rows failed with `3819` and left the previous schema. A column check that named another column failed with `3813`. A parent delete failed with `1451`. A window, a CTE, a `UNION`, and a left join returned the expected rows.

Variations attempted: a missing table, two unnamed unique keys, `SHOW INDEX`, catalog column lists, and `ORDER BY ORDINAL_POSITION`.

This journey produced [#542](https://github.com/jonbaldie/database/issues/542), [#543](https://github.com/jonbaldie/database/issues/543), and [#544](https://github.com/jonbaldie/database/issues/544). The unnamed-unique collision in [#484](https://github.com/jonbaldie/database/issues/484) still fails with `1061` and name `n8_a_unique`.

### C. An operator backs up a live database, restores it, and stops the server

**Goal:** create a backup, inspect it, restore it to a new directory, and read the committed rows from that copy. Then stop the original server and start it again. Expectation: [`docs/operator-automation.md`](../operator-automation.md) and [`docs/server-configuration.md`](../server-configuration.md).

Ordinary path succeeded. The backup excluded a row inserted after the backup. Restore created a new instance id and a stopped directory. The restored server returned the backed-up row and not the later row. `data inspect` and `data validate` used logical component names and included `checked_at`. A truncated backup was `invalid_artifact` with exit code `5`. A second `serve` on a live directory was `precondition` with exit code `3`. `shutdown --yes` stopped the server. An insert held open across shutdown was gone after restart. Committed rows remained.

Variations attempted: a wrong password, restore onto a non-empty directory, diagnostics `GET`, `HEAD`, and `POST`, a non-loopback listener, and `--result=json` compared with human output and `--format=json`.

This journey produced [#545](https://github.com/jonbaldie/database/issues/545).

## Confirmed defect 1 — ordinal order is text order (#542)

**User impact.** A client that orders `information_schema.COLUMNS` by `ORDINAL_POSITION` gets column 10 before column 2.

**Replay.** Create `wide` with columns `c01` through `c12`. `ORDER BY ORDINAL_POSITION` returns `c01`, `c10`, `c11`, `c12`, then `c02`. `ORDER BY ORDINAL_POSITION + 0` on that catalog view fails with `1054`. The same expression on base table `p` succeeds.

**Repeat observations.** Seen first on `STATISTICS` column metadata, then replayed on `wide`.

**Expected.** An integer ordinal sorts numerically. MySQL 8.4.11 returns `c01` through `c12`.

Evidence: [replay-order.jsonl](2026-10-10-sql-catalog-operator/evidence/replay-order.jsonl).

## Confirmed defect 2 — catalog views omit contracted columns (#543)

**User impact.** A query for `ENGINE`, `TABLE_ROWS`, or `IS_NULLABLE` fails with `1054`. `SHOW COLUMNS` still reports nullability and key class.

**Replay.** `information_schema.TABLES` exposes only `TABLE_SCHEMA`, `TABLE_NAME`, `TABLE_TYPE`, and `AUTO_INCREMENT`. `information_schema.COLUMNS` exposes only `TABLE_SCHEMA`, `TABLE_NAME`, `COLUMN_NAME`, `ORDINAL_POSITION`, `DATA_TYPE`, `COLUMN_TYPE`, and `EXTRA`.

**Repeat observations.** Column lists and the `1054` failures were seen twice on the same instance.

**Expected.** [`docs/catalog-metadata.md`](../catalog-metadata.md) requires `ENGINE`, `TABLE_ROWS`, nullability, default, and key classification. Unsupported physical facts are `NULL`, not absent columns.

Evidence: [catalog-spot.jsonl](2026-10-10-sql-catalog-operator/evidence/catalog-spot.jsonl) and [replay-catalog.jsonl](2026-10-10-sql-catalog-operator/evidence/replay-catalog.jsonl).

## Confirmed defect 3 — missing-table schema changes use the wrong error number (#544)

**User impact.** A migration cannot tell a missing table from a duplicate key name, a rename failure, or a missing index.

**Replay.** On database `app`, with no table `no_such`:

| Statement | Actual | MySQL 8.4.11 |
| --- | --- | --- |
| `CREATE INDEX missing_idx ON no_such (id)` | `1061` `42000` | `1146` `42S02` |
| `ALTER TABLE no_such ADD COLUMN x INT NULL` | `1025` `HY000` | `1146` `42S02` |
| `DROP INDEX missing_idx ON no_such` | `1091` `42000` | `1146` `42S02` |
| `DROP TABLE no_such` | `1051` `42S02` | `1051` `42S02` |

**Repeat observations.** `CREATE INDEX` and `ALTER TABLE ADD COLUMN` failed the same way three times. The other statements in the issue were replayed once in a fresh database.

**Expected.** A missing table on these supported schema statements returns `1146` and `42S02`.

Evidence: [replay-ddl-errors.jsonl](2026-10-10-sql-catalog-operator/evidence/replay-ddl-errors.jsonl).

## Confirmed defect 4 — JSON serve hides the TLS warning until shutdown (#545)

**User impact.** Automation that uses `--result=json` does not see `UNSAFE_NON_TLS_LISTENER` while a non-loopback server is ready. The warning arrives only after the process stops.

**Replay.** `database serve --result=json --progress=json` with `--mysql-listen-address=0.0.0.0:13309`. After `/ready` returns 200, standard output is empty and the ready progress record has no warning. Human output on the same directory prints the warning before `database: ready`. `--format=json` prints a `database.lifecycle/v1` ready record with the warning before stop.

**Repeat observations.** Seen on port `13308`, then replayed on port `13309`.

**Expected.** The ready lifecycle record carries the warning when the server becomes ready. Human output already does this.

Evidence: [serve-result-json-ready.stderr](2026-10-10-sql-catalog-operator/evidence/serve-result-json-ready.stderr), [serve-result-json-terminal.json](2026-10-10-sql-catalog-operator/evidence/serve-result-json-terminal.json), and [serve-human-ready.stdout](2026-10-10-sql-catalog-operator/evidence/serve-human-ready.stdout).

## Confirmed defect 5 — SQL version stays at 0.2.0-dev (#546)

**User impact.** `VERSION()` and `EXPLAIN` identify a development build. `database version` for the same binary reports `0.2.18`.

**Replay.** `database version` prints `database 0.2.18`. `SELECT VERSION()` returns `8.4.11-database-0.2.0-dev`. `EXPLAIN FORMAT=JSON` has `"server_version":"0.2.0-dev"`.

**Repeat observations.** `VERSION()` was the same on the first session, on the restored copy, and after shutdown and a new `serve`.

**Expected.** [`docs/query-explanation/README.md`](../query-explanation/README.md) says `server_version` is the server release that produced the explanation. That release is `0.2.18`.

Evidence: [version.txt](2026-10-10-sql-catalog-operator/evidence/version.txt) and [after-restart.jsonl](2026-10-10-sql-catalog-operator/evidence/after-restart.jsonl).

## Rejected with evidence

These candidates were exercised and did not violate the grounded contract.

- The README prepared query, the corrected insert, and `EXPLAIN ANALYZE FORMAT=JSON` succeeded. The explanation had `format_version` 1 and `mode` `analyze`.
- A bad check or foreign key left earlier rows in place. A failed `ALTER TABLE ADD CONSTRAINT` left the previous schema.
- `ROLLBACK` removed an uncommitted insert. A second session did not see an uncommitted update. `FOR UPDATE NOWAIT` returned `3572`. `FOR UPDATE SKIP LOCKED` omitted the locked row. A blocked `UPDATE` returned `1205`. After commit, the other session saw the new value. Evidence: [locks.jsonl](2026-10-10-sql-catalog-operator/evidence/locks.jsonl).
- Backup create and inspect succeeded. Restore of a 100-byte truncation returned exit class `invalid_artifact`, exit code `5`, and `target_state` `not_created`. Evidence: [truncated-restore.json](2026-10-10-sql-catalog-operator/evidence/truncated-restore.json).
- Restore onto a non-empty directory returned `precondition`. A wrong password returned exit code `4` and summary `connection failed`.
- `data inspect` used logical names such as `catalog` and `row_store`. `data validate` included `checked_at` and no storage file names.
- A second `serve` on a live directory returned exit code `3` and summary `data directory is already in use`. Evidence: [second-serve.json](2026-10-10-sql-catalog-operator/evidence/second-serve.json).
- `shutdown --yes` stopped the server. An insert held open across that shutdown was absent after restart. Committed rows remained. Evidence: [after-restart.jsonl](2026-10-10-sql-catalog-operator/evidence/after-restart.jsonl).
- Diagnostics `GET` and `HEAD` returned `200` for `/live`, `/ready`, and `/metrics`. `POST /ready` returned `405`. `HEAD /nope` returned `404`. An earlier `curl -X HEAD` timeout was a client observation error. `curl -I` completed.
- `EXPLAIN FORMAT=JSON FOR CONNECTION` returned a partial snapshot of an `UPDATE` waiting on a row lock, with `mode` `snapshot` and a lock wait in the runtime evidence.
- `DROP TABLE` on a missing table returned `1051`, which matches MySQL 8.4.11 for that statement.

## Usability observations

These are observations from the journeys, not filed defects.

- `BETWEEN` and a value-list `IN` predicate fail with `1064` `unsupported expression`. A user who writes ordinary MySQL predicates has to rewrite them as comparisons or subqueries. The SQL contract does not name those forms as supported.
- `database --help` does not list `backup`, `restore`, `upgrade`, or `data`. `database backup --help` fails as an unsupported backup operation. The commands work when the operator already knows the contract.
- Unnamed unique keys are still named `<table>_<column>_unique`. That is open issue [#484](https://github.com/jonbaldie/database/issues/484). `AUTO_INCREMENT` inserts still report last insert ID `0` while storing the generated id. That is open issue [#448](https://github.com/jonbaldie/database/issues/448).

## Blocked and unexplored areas

No selected journey was blocked.

Not explored: TLS listeners, PHP, Node.js, Python, and Java clients, account grants other than the initial administrator, upgrade of an old data directory, statement timeout under a large scan, and a two-session deadlock. The project-local `verify-database` skill is a broken symlink to `.agents/skills/verify-database`, so this pass drove the public CLI and the Go driver directly.

## Limitations

The Go driver text protocol returns some integers as strings. Numeric sort bugs were judged from result order, not from that driver representation. `ORDER BY id` on a base table returned numeric order, so the driver did not invent the catalog sort. No Docker container, image, or volume was created.
