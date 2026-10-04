# Exploratory testing pass: configuration, limits, and schema evolution

**Date:** 2026-10-03

**Build:** `database` v0.2.16, commit `68bb55a58681aaf03aba7579a1ab756889c0e9e4`, darwin/arm64

**Surface driven:** the operator CLI, the diagnostics listener, and the MySQL classic wire protocol. The tools were the `verify-database` skill and small Go programs that use `github.com/go-sql-driver/mysql` v1.9.3. The `verify-database` skill was removed from the repository in `778e830`. For this pass it was restored as an untracked copy from `778e830^` and was not committed. No `internal/` package was used to get a result. Source was read only to explain a failure that was already observed.

**Instances:** disposable `control.sh` runs `1790987883-18329`, `1790987905-23334`, `1790988623-64877`, `1790988654-73140`, and `1790988688-81668`. The lead replay of the schema and limits candidates used run `1790988688-81668`. Also used:

- a dedicated server with `--max-prepared-stmt-count 2 --max-allowed-packet 1024 --idle-session-timeout-ms 3000 --idle-in-transaction-timeout-ms 2000`
- fresh data directories for the operator replays

Each run had its own generated password and data directory.

**Evidence:** these directories are under `/tmp/verify-database-evidence/`:

- `1790987883-18329/`
- `1790987905-23334/`
- `1790988623-64877/`
- `1790988654-73140/`
- `1790988688-81668/`
- `journey-a-20261003/`
- `lead-b-20261003/`

The run directories were removed. The evidence directories were kept.

This is an exploratory pass, not a conformance run. It attempted three user journeys and followed surprises. It does not claim coverage of the v0.1 contracts.

## Result

Sixteen product defects confirmed and filed:

| # | Issue | Area |
| --- | --- | --- |
| 1 | [#484](https://github.com/jonbaldie/database/issues/484) | Unnamed `UNIQUE` keys get non-MySQL names that collide |
| 2 | [#485](https://github.com/jonbaldie/database/issues/485) | `information_schema` constraint and `STATISTICS` views lack MySQL 8.4 columns |
| 3 | [#486](https://github.com/jonbaldie/database/issues/486) | `SHOW INDEX` reports `Null` wrongly and filters on hidden values |
| 4 | [#487](https://github.com/jonbaldie/database/issues/487) | `SHOW INDEX` lists `PRIMARY` after a later secondary index |
| 5 | [#488](https://github.com/jonbaldie/database/issues/488) | `DROP INDEX` cannot drop a server-named `UNIQUE` key |
| 6 | [#489](https://github.com/jonbaldie/database/issues/489) | Schema-change failures return generic error numbers |
| 7 | [#490](https://github.com/jonbaldie/database/issues/490) | `FOREIGN KEY` accepts incompatible column types |
| 8 | [#491](https://github.com/jonbaldie/database/issues/491) | A column-level `CHECK` that references another column is accepted |
| 9 | [#492](https://github.com/jonbaldie/database/issues/492) | Idle-session and idle-in-transaction timeouts are never enforced |
| 10 | [#499](https://github.com/jonbaldie/database/issues/499) | Oversized packets drop the connection without `ER_NET_PACKET_TOO_LARGE` |
| 11 | [#493](https://github.com/jonbaldie/database/issues/493) | `EXPLAIN ANALYZE` fails with `1114` for a `SELECT` that succeeds |
| 12 | [#494](https://github.com/jonbaldie/database/issues/494) | Error `1461` uses SQLSTATE `HY000`, not `42000` |
| 13 | [#495](https://github.com/jonbaldie/database/issues/495) | `SET time_zone='-14:00'` is accepted |
| 14 | [#496](https://github.com/jonbaldie/database/issues/496) | `serve` on an uninitialized data directory exits `1` |
| 15 | [#497](https://github.com/jonbaldie/database/issues/497) | `serve` progress emits `stopped`, and its details omit required facts |
| 16 | [#498](https://github.com/jonbaldie/database/issues/498) | `init` failure prints a JSON envelope when no `--result` is given |

Each issue has its own starting conditions, replay steps, expected and actual outcomes, and evidence paths. This report gives a summary of each one.

## Journeys attempted

### A. An operator configures, secures, and runs a server

**Goal:** set the server configuration from files, environment, and flags, then validate and serve it with TLS. A further goal was to watch its lifecycle from automation. Expectation: [`docs/server-configuration.md`](../server-configuration.md) and [`docs/operator-automation.md`](../operator-automation.md).

The ordinary path succeeded:

- Source precedence was correct, and every invalid form exited `2`.
- `serve` applied the effective settings.
- TLS 1.2 and TLS 1.3 connected.
- `UNSAFE_NON_TLS_LISTENER` appeared only when expected.
- Diagnostics returned `405` and `404` as documented.
- A port in use and a locked data directory exited `3`.

Variations attempted:

- an empty, missing, and uninitialized data directory
- `--result=json`, `--format=json`, and `--progress=json`
- SIGTERM shutdown
- idle-timeout settings
- `--log-format`
- a missing TLS key
- overlapping listeners
- `init` without a password option

Known issue [#462](https://github.com/jonbaldie/database/issues/462) reproduced.

### B. An application works inside the session limits

**Goal:** an application tightens its session settings and stays inside the server limits. When it reaches a limit, it gets the documented error. Expectation: [`docs/session-settings.md`](../session-settings.md), [`docs/server-configuration.md`](../server-configuration.md), the limits record [#20](https://github.com/jonbaldie/database/issues/20), and [`docs/query-explanation/README.md`](../query-explanation/README.md).

The ordinary path succeeded:

- `SET` validation returned `1231`, `1238`, and `1568`. Ceilings held, and `COM_RESET_CONNECTION` reset the session.
- The statement deadline returned `3024`, and `KILL QUERY` returned `1317`.
- Spilled results were correct, and memory exhaustion returned `1114`.
- The connection cap returned `1040`, and the prepared-statement cap returned `1461`.
- An inbound packet of exactly 1024 bytes was accepted.

Variations attempted:

- an idle session and an idle transaction holding a row lock
- an oversized inbound query and an oversized result row
- `EXPLAIN ANALYZE` under a tight memory limit
- time-zone range edges
- old variable aliases
- concurrent prepares at the cap

### C. A developer evolves a schema and relies on its constraints

**Goal:** create tables with keys and constraints, change them with `ALTER TABLE`, inspect them, and keep them across a restart. Expectation: [`docs/mysql-sql-behaviour.md`](../mysql-sql-behaviour.md) and [`docs/catalog-metadata.md`](../catalog-metadata.md).

The ordinary path succeeded:

- `NOT NULL`, `DEFAULT`, primary key, `UNIQUE`, `CHECK`, and `FOREIGN KEY` were enforced.
- Writes to a referenced parent returned `1451`, and `TRUNCATE` of a referenced parent returned `1701`.
- `ALTER TABLE ... ADD` of a constraint failed on violating data and kept the old schema.
- Multi-row statements were atomic.
- `SHOW CREATE TABLE` was identical after `control.sh restart`. Its output replayed cleanly, and constraints were still enforced after the restart.

Variations attempted:

- unnamed and colliding keys
- `DROP INDEX` of server-named keys
- incompatible foreign-key types and cross-column `CHECK`
- `REPLACE` and `ON DUPLICATE KEY UPDATE` counts
- the 3072-byte key limit (`1071`) and the 64-index limit (`1069`)
- `information_schema` and `SHOW INDEX` inspection

Known issues [#202](https://github.com/jonbaldie/database/issues/202) and [#448](https://github.com/jonbaldie/database/issues/448) reproduced.

## Confirmed defects

All defects below were replayed from a known starting state at least two times, with the same result each time. The issue holds the full replay.

### Schema evolution (#484–#491)

- **#484:** `UNIQUE (a), UNIQUE (a, b)` fails with `1061 duplicate constraint name 'n8_a_unique'`. MySQL names these keys `a` and `a_2`.
- **#485:** `information_schema` `TABLE_CONSTRAINTS`, `REFERENTIAL_CONSTRAINTS`, `KEY_COLUMN_USAGE`, and `STATISTICS` reject MySQL 8.4 columns with `1054`.
- **#486:** `SHOW INDEX` reports `Null` as `NULL` for `NOT NULL` columns, and `SHOW INDEX ... WHERE Non_unique=0` fails with `1292`.
- **#487:** `SHOW INDEX` lists `PRIMARY` after a secondary index that was created later.
- **#488:** `ALTER TABLE p DROP INDEX p_code_unique` fails with `1025`, although `SHOW INDEX` lists that name.
- **#489:** schema-change failures return generic numbers (`1025`, `1069`, `1061`, `3813`, `1054`, `3522`), where MySQL 8.4 returns specific ones such as `1091`, `1070`, `3822`, `6125`, `3820`, and `1176`.
- **#490:** a `VARCHAR` or `DATE` column can reference an `INT` parent key. MySQL returns `3780`.
- **#491:** a column-level `CHECK` that references another column is accepted. MySQL returns `3813`.

Evidence: `1790988688-81668/journey-c-replay.jsonl`, `1790988623-64877/confirm-r1.jsonl`, and `confirm-r2.jsonl`.

### Idle timeouts are not enforced (#492)

**Impact:** an abandoned transaction keeps its row locks, and other sessions get `1205` until an operator kills it.

**Replay:** start `serve` with `--idle-in-transaction-timeout-ms 2000` and `--idle-session-timeout-ms 3000`. `@@idle_*` returns `0`. Session A runs `BEGIN; UPDATE` and stays idle for 3.5 s. Session B's `SELECT ... FOR UPDATE` gets `1205`, and session A is still usable. An idle session survives 4.5 s.

**Expected:** #20 says an idle transaction is rolled back and its session is closed. An idle session with no transaction is closed. `docs/server-configuration.md` says that zero never disables a safeguard.

Journeys A and B found this defect independently. Evidence: `lead-b-20261003/replay-1.txt`, `replay-2.txt`, and `journey-a-20261003/replay-1.txt`.

### Oversized packets drop the connection (#499)

**Impact:** clients get `invalid connection`, not error `1153`. One large result row also ends the session.

**Replay:** with `max_allowed_packet=1024`, send a query of about 1130 bytes. On a new session, select one row of about 1200 bytes.

**Expected:** from #20, inbound input returns `1153` and closes the session. An outbound row fails the statement with `1153` and keeps the session.

Evidence: `lead-b-20261003/replay-1.txt` and `replay-2.txt`.

### EXPLAIN ANALYZE fails where the query succeeds (#493)

With `execution_memory_limit_bytes=100000` and a table of 1280 rows, `SELECT a.id,b.id FROM n a CROSS JOIN n b WHERE a.id<50` succeeds. `EXPLAIN ANALYZE` of the same statement returns `1114`. The analysis path does not stream rows (`internal/mysql/explain.go:129`). Evidence: `lead-b-20261003/replay-control.txt`.

### Error 1461 uses the wrong SQLSTATE (#494)

The third prepare with `max_prepared_stmt_count=2` returns `1461 (HY000)`. MySQL uses SQLSTATE `42000`. Evidence: `lead-b-20261003/replay-1.txt`.

### Out-of-range time zone is accepted (#495)

`SET time_zone='-14:00'` succeeds. The documented range is `-13:59` to `+14:00`. Evidence: `lead-b-20261003/replay-control.txt`.

### serve exit code for an uninitialized directory (#496)

An empty or missing data directory exits `1` in human and `--format=json` modes. The JSON envelope reports `operation_failed` with code `1`. With `--result=json`, the same failure exits `6`. Exit code `1` is not in the exit-class table. Evidence: `lead-b-20261003/replay-a-c1-c5.txt`.

### serve progress and details (#497)

`--progress=json` emits a fourth phase, `stopped`, which is outside the closed `serve` vocabulary. The success details have only `data_directory`, `recovered`, and `state`. They do not include instance identity, readiness and stopping times, or shutdown reason. Evidence: `lead-b-20261003/replay-a-c3-c4.txt`.

### init prints JSON by default (#498)

`database init --data-directory=x --initial-account admin` prints a JSON result envelope, but the default is `--result=human`. Its usage text names options that `init` does not take. Evidence: `lead-b-20261003/replay-a-c1-c5.txt`.

## Rejected with evidence

- **Self-referencing foreign keys:** they were enforced, and the final state was correct.
- **`AUTO_INCREMENT`:** it left no gaps in the replayed cases.
- **`ON DUPLICATE KEY UPDATE v=v`:** it returned `1235`. The SQL contract documents this.
- **`REPLACE` on a referenced parent:** it returned `1451`, which matches MySQL with InnoDB.
- **The 64-index limit:** the limit counts the primary key, which matches MySQL.
- **`STATISTICS.CARDINALITY`:** it equals the exact row count. No contract requires an estimate.
- **Concurrent prepares at the cap:** they never exceeded the cap. The suspected race did not reproduce.
- **A 1019-byte result:** a suspected packet failure at this size came from metadata overhead in the explorer's own count. A 1024-byte inbound packet was accepted.
- **Driver behaviour:** `ResetSession` does not send `COM_RESET_CONNECTION`, and a cancelled context leaves a bad connection. Both are behaviours of `go-sql-driver/mysql`, not the server.

## Unresolved

- **Reported peak memory:** under `execution_memory_limit_bytes=2000`, `EXPLAIN ANALYZE SELECT id FROM n ORDER BY pad, id` succeeds, and the sort reports `actual_peak_memory_bytes` `5293`. The explorer saw a root `peak_memory_bytes` of `9005`. No contract says that the reported peak stays at or below the limit. Evidence: `lead-b-20261003/replay-b4.txt` and `1790987883-18329/18-explain-peak-vs-limit.jsonl`.
- **`tx_isolation`:** `SELECT @@tx_isolation` succeeds, and `SHOW VARIABLES` lists it. `SET tx_isolation` fails with `1238`. `docs/session-settings.md` says that "old aliases ... fail". The source publishes this name as read-only on purpose (`internal/mysql/session_settings.go:65,90`). The owner must decide whether the contract or the code is correct.
- **`autocommit` inside a transaction:** `SET autocommit=1` inside `BEGIN` commits the transaction. After `autocommit=0; SELECT 1`, transaction characteristics return `1568`. Both need a comparison with a live MySQL 8.4 server.
- **`GROUP_CONCAT`:** `GROUP_CONCAT(... ORDER BY ... SEPARATOR ...)` inside an `information_schema` query returned `1064`. It was not reduced.
- **`--log-format`:** it has no visible effect, and standard error stays empty. The contract defers the log sink to [#31](https://github.com/jonbaldie/database/issues/31).
- **Configuration and init edge cases:**
  - When the TLS key is missing, `config validate` shows the private-key path in its diagnostics.
  - `0.0.0.0:P` together with `127.0.0.1:P` passes validation.
  - `//live` returns `307`, not `404`.
  - A corrupt `instance.json` is reported as "not initialized".
  - `init` accepts a relative directory and succeeds without `--initial-account`.

  None of these was checked against a precise contract sentence.

## Usability observations

These are observations from the journeys, not filed defects.

- The MySQL version string is `8.4.11-database-0.2.0-dev` in a `0.2.16` build.
- `serve --help` lists only some of the flags that `serve` accepts.
- No log record tells an operator about an oversized packet or a closed idle session.
- A `405` diagnostics response has no `Allow` header.
- The `config validate` result has no explicit validity field.
- `CONNECTION_ID()`, user variables, SQL `PREPARE`, derived tables, and `REPEAT()` are not supported, so ordinary MySQL test scripts need changes.
- A subquery in `UPDATE ... SET` returns `1054 Unknown column 'SELECT'`, which does not tell the user that the form is unsupported.
- Aggregates do not stream, so `SELECT COUNT(*)` over 1280 rows fails with `1114` under a 100000-byte limit.

## Not explored

This pass did not drive these areas:

- `backup`, `restore`, and `upgrade`
- `shutdown` through the operational session
- TLS `verify-full` client profiles
- `password-stdin`
- the PHP, Node, Python, and Java client profiles
- aggregate execution and temporary-storage budgets shared by many sessions
- `ER_NET_PACKET_TOO_LARGE` for prepared long data

## Cleanup

These steps were done:

- `control.sh clean` removed each run directory.
- The dedicated limits server received `SIGTERM` and exited.
- The lead replay directories and the scratch worktree were removed.

The evidence directories under `/tmp/verify-database-evidence/` were kept. No generated artifact from this pass was over 100 MB.
