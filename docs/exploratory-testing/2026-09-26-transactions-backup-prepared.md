# Exploratory testing pass: transactions, backup, and prepared statements

**Date:** 2026-09-26

**Build:** `database` v0.2.14, commit `6f6bd5505befcc6b37b61fa6340a0a84dba7c63e`, build identity `local`, darwin/arm64, `go1.26.6`

**Surface driven:** the operator CLI and the MySQL classic wire protocol, through the project-local `verify-database` skill and a small Go program that uses `github.com/go-sql-driver/mysql` v1.9.3. No `internal/` package was used to obtain a result. Source was read only to explain an already-observed failure.

**Instance:** one disposable run, `1790382956-9741`, with a generated password and its own data directory. A restored copy was served on separate loopback ports and stopped before cleanup. `control.sh doctor` passed before the journeys and again after the transaction restart.

**Evidence:** `/tmp/verify-database-evidence/1790382956-9741/` (324K at cleanup). The run directory was removed. The evidence directory was kept.

This is an exploratory pass, not a conformance run. It attempted three user journeys and followed surprises. It does not claim coverage of the v0.1 contracts.

## Result

Four product defects confirmed and filed:

| # | Issue | Area |
| --- | --- | --- |
| 1 | [#445](https://github.com/jonbaldie/database/issues/445) | Online command diagnostics distinguish a valid password from insufficient permission |
| 2 | [#446](https://github.com/jonbaldie/database/issues/446) | `database restore` classifies a corrupt backup as `operation_failed` |
| 3 | [#447](https://github.com/jonbaldie/database/issues/447) | `data inspect` and `data validate` results violate the operator contract |
| 4 | [#448](https://github.com/jonbaldie/database/issues/448) | `AUTO_INCREMENT` inserts report last insert ID `0` |

The ordinary transaction, backup, and prepared-query paths succeeded. Those rejected candidates are listed under [Rejected with evidence](#rejected-with-evidence).

## Journeys attempted

### A. An application groups writes and shares the table with another session

**Goal:** commit a group of changes, undo a mistake with a savepoint, and keep a second session from writing over a locked row. Expectation: [`docs/mysql-sql-behaviour.md`](../mysql-sql-behaviour.md) and [`docs/session-settings.md`](../session-settings.md).

Ordinary path succeeded. Variations attempted: a failed statement inside an open transaction, an unknown and a released savepoint, a replaced savepoint name, `START TRANSACTION` and `COMMIT WORK`, `autocommit = OFF`, a read-only transaction, `REPEATABLE-READ` and `READ-COMMITTED`, `FOR UPDATE NOWAIT`, `FOR SHARE NOWAIT`, `SKIP LOCKED`, a lock-wait timeout, a deadlock, a lock kept after `ROLLBACK TO SAVEPOINT`, and an open insert discarded by server stop.

### B. An operator backs up a live database and restores it

**Goal:** create a backup of a running server, inspect it, restore it to a new directory, and read the committed rows from that copy. Expectation: [`docs/operator-automation.md`](../operator-automation.md).

Ordinary path succeeded. The backup excluded an uncommitted insert and a row committed after the backup. Variations attempted: restore onto a non-empty directory, backup to an existing output path, a wrong password, a missing account, a locked account, an account with only `OPERATIONAL_OBSERVATION`, an account with `OPERATIONAL_CONTROL`, a truncated archive, an empty archive, a non-tar archive, and `data inspect` / `data validate` on stopped and serving directories.

This journey produced [#445](https://github.com/jonbaldie/database/issues/445), [#446](https://github.com/jonbaldie/database/issues/446), and [#447](https://github.com/jonbaldie/database/issues/447).

### C. An application follows the README trial with a prepared query

**Goal:** create the `trial.tasks` table from [`README.md`](../../README.md), run the prepared `priority >= ?` query, and read `EXPLAIN ANALYZE FORMAT=JSON`. Then correct a bad insert and continue on the same session.

Ordinary path succeeded. The prepared query returned `2: test query behaviour`. The explanation document started with `format_version` 1 and `mode` `analyze`. A non-numeric priority failed with `1366`, and the corrected insert was stored. Text and prepared reads of the same predicate returned the same rows.

The follow-up `AUTO_INCREMENT` insert produced [#448](https://github.com/jonbaldie/database/issues/448).

## Confirmed defect 1 — authentication diagnostics are distinguishable (#445)

**User impact.** `backup create` and `shutdown` reveal whether the password was accepted. A wrong password, a missing account, and a locked account all say `connection failed`. The same account with the correct password and no `OPERATIONAL_CONTROL` says something else.

**Replay.** Create `observer` with `OPERATIONAL_OBSERVATION` only. `backup create` as `observer` with the correct password returns exit code `4` and summary `Error 1227 (42000): access denied`. The same command with the wrong password returns exit code `4` and summary `connection failed`. `shutdown --yes` returns `shutdown request failed` for the correct observer password and `connection failed` for the wrong admin password.

**Repeat observations.** Replayed both commands. After `ALTER USER 'observer' ACCOUNT LOCK`, backup create returned `connection failed`, so the product can hide the distinction and does not do so for a permission failure.

**Expected.** [`docs/operator-automation.md`](../operator-automation.md): authentication diagnostics never distinguish a missing or suspended account, a wrong password, or insufficient permission.

Evidence: `backup/observer-backup.json`, `backup/replay-observer.json`, `backup/replay-wrong.json`, `backup/shutdown-observer.json`, `backup/shutdown-wrong.json`, `backup/locked-backup.json`.

## Confirmed defect 2 — corrupt restore uses the wrong exit class (#446)

**User impact.** Automation that branches on the stable exit class treats a bad backup as work that started and failed, not as an unusable artifact. The failure `details` object is empty, so it does not say that no directory was created.

**Replay.** `backup inspect` of a 100-byte truncation, an empty file, and a non-tar file returns exit class `invalid_artifact` and exit code `5`. `restore` of each file returns exit class `operation_failed`, exit code `6`, and `"details":{}`. No target directory is created.

**Repeat observations.** All three bad inputs failed the same way on the first attempt. Inspect of the same bytes returned exit code `5`.

**Expected.** Exit class `invalid_artifact` and exit code `5` for an incomplete, corrupt, or untrustworthy backup. A failed artifact-producing command reports cleanup, output usability, and terminal state.

Evidence: `backup/truncated-inspect.json`, `backup/truncated-restore.json`, `backup/empty-inspect.json`, `backup/empty-restore.json`, `backup/garbage-inspect.json`, `backup/garbage-restore.json`.

## Confirmed defect 3 — data command results expose internal files (#447)

**User impact.** `data inspect` and `data validate` name internal files in `details`. `data validate` also omits the check time required in those details.

**Replay.** On a serving data directory, inspect `details.entries` includes `.database-state`, `.running.lock`, `catalog.json`, `instance.json`, and `rows`. Validate `details.examined` includes `rows/wal.log`, `rows/tables.meta`, and `rows/checkpoint.dat`, each with a hash. There is no check-time field in validate `details`.

**Repeat observations.** Replayed on a restored stopped directory, again after that copy was serving, and on the original serving directory.

**Expected.** Diagnostics and details never expose internal file layout. Successful `data validate` details include identity, directory, integrity outcome, check time, and structured findings.

Evidence: `backup/restored-inspect.json`, `backup/restored-validate.json`, `backup/replay-inspect.json`, `backup/replay-validate.json`, `backup/live-inspect.json`, `backup/live-validate.json`.

## Confirmed defect 4 — generated ids are stored but not reported (#448)

**User impact.** An application that inserts a row and reads the client last insert ID gets `0`. The row exists and has a real id. The README trial uses `database/sql`, whose `LastInsertId` comes from the OK packet.

**Replay.**

```sql
CREATE TABLE ids (id BIGINT PRIMARY KEY AUTO_INCREMENT, label VARCHAR(20) NOT NULL);
INSERT INTO ids (label) VALUES ('one');
INSERT INTO ids (label) VALUES ('two'), ('three');
SELECT id, label FROM ids ORDER BY id;
```

The inserts report `rows_affected` 1 and 2, and `last_insert_id` 0. The following read returns ids `1`, `2`, and `3`. A server-prepared insert into a different auto-increment table also returned `LastInsertId` 0 while storing id `1`.

**Repeat observations.** Seen first through the Go driver, then through text SQL, then replayed on a new table.

**Expected.** MySQL 8.4.11 reports the generated id in the OK-packet last insert ID. `AUTO_INCREMENT` is a supported construct, and an unlisted difference inside that surface is a defect. `LAST_INSERT_ID()` is not part of this defect: that name is outside the closed function registry, and `1305` matches that boundary.

Evidence: `prepared/replay-auto.jsonl` and the prepared-trial output in `prepared/`.

## Rejected with evidence

These candidates were exercised and did not violate the grounded contract.

- `BEGIN` / `COMMIT` made the inserted row visible. `ROLLBACK` removed it. `ROLLBACK TO SAVEPOINT` kept the earlier row and dropped the later one. A duplicate-key `1062` left the transaction open, and a later `ROLLBACK` removed the earlier uncommitted row.
- An unknown savepoint and a released savepoint failed with `1305`. Replacing a savepoint name moved the savepoint forward, so a later `ROLLBACK TO` kept the row inserted before the replacement. That matches the contract.
- `SET transaction_read_only = ON` rejected an insert with `1792` and left the table unchanged. `autocommit = OFF` made an insert visible in the session, and `ROLLBACK` removed it.
- Session defaults were `REPEATABLE-READ`, `autocommit` `ON`, and `lock_wait_timeout_ms` `5000`.
- A second session did not see an uncommitted update. `FOR UPDATE NOWAIT` and `FOR SHARE NOWAIT` against the held row failed with `3572`. `FOR UPDATE SKIP LOCKED` omitted the held row and returned the others. After the holder session closed, the committed value was unchanged.
- `ROLLBACK TO SAVEPOINT` did not release the lock on the undone row or the lock taken before the savepoint. Both `NOWAIT` reads failed with `3572`.
- A blocked `UPDATE` failed with `1205`. The waiter transaction stayed open, and a later insert in that transaction committed. The locked row stayed at its committed value.
- `REPEATABLE-READ` hid a commit made by another session. `READ-COMMITTED` saw that commit. A two-session update cycle returned `1213` and SQLSTATE `40001` to the session that closed the cycle. The other session could then roll back. The locked rows were unchanged after that rollback.
- An insert in an open transaction was visible to that session and absent after `control.sh restart`. Committed rows survived the restart.
- Online `backup create` returned exit code `0`, `complete: true`, and the documented details. Progress phases were `connecting`, `capturing`, `writing`, `validating`, with one shared `operation_id`. The restored copy contained the committed rows from backup time, including row `1` total `252` and row `15`. It did not contain uncommitted row `99` or row `16`, which was committed after the backup.
- `backup inspect` reported `integrity: valid`, `completeness: true`, and the source instance id. `restore` created a new instance id, state `stopped`, and the source instance id. `data validate` on that stopped copy reported `valid: true` and no findings. `data inspect` reported `state: stopped`, `recovery_required: false`, and `upgrade_required: false`.
- Restore onto a non-empty directory returned exit class `precondition` and exit code `3`. Backup onto an existing output path did the same. A wrong password, a missing account, and a locked account all returned `connection failed` and exit code `4`, with no output file. An account with `OPERATIONAL_CONTROL` created a backup. Accounts without that grant were denied with exit code `4`. The denial is correct. The diagnostic text is [#445](https://github.com/jonbaldie/database/issues/445).
- `data inspect` on the live source reported `state: serving`. Closed issue [#311](https://github.com/jonbaldie/database/issues/311) did not regress.
- The README prepared query and `EXPLAIN ANALYZE FORMAT=JSON` succeeded. A bad prepared integer failed with `1366`, and the same session accepted the corrected insert. Text and prepared reads matched.
- `SELECT LAST_INSERT_ID()` failed with `1305`. That function is not in the closed registry, so this is not a defect.
- `WHERE id IN (1, 2)` failed with `1064`. That form is already tracked as enhancement [#202](https://github.com/jonbaldie/database/issues/202). The supported `IN` form in the SQL contract is the subquery form. This pass did not refile it.

## Usability observations

These are observations from the journeys, not filed defects. The operator contract says human wording is not an automation contract.

- `database --help` lists `init`, `version`, `serve`, `shutdown`, and `config`. It does not list `backup`, `restore`, `upgrade`, or `data`, although those commands run. `database backup --help` does not show help. It fails as an unsupported backup operation. An operator who follows the tool's own help text cannot discover the documented recovery commands.
- `data validate` accepts a serving directory and reports `valid: true`. It does not say that the instance is serving. `data inspect` does report `serving`. The contract describes `data validate` as an offline command that uses the explicit path. It does not say the command must refuse a live directory, so this was not filed.

## Not explored

This pass did not drive `upgrade`, configuration source precedence, `password-stdin`, TLS `verify-full`, statement timeout `3024`, execution-memory or temporary-storage exhaustion `1114`, client cancellation `1317` beyond session close, or the PHP, Node, Python, and Java client profiles.

## Cleanup

The restored `serve` process received `SIGTERM` and exited. `control.sh clean 1790382956-9741` removed the run directory. Evidence remains at `/tmp/verify-database-evidence/1790382956-9741/` and was 324K. No generated artifact from this pass was over 100 MB.
