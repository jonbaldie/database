# Exploratory testing pass: set operations, transactions, indexes, and schema evolution

**Date:** 2026-09-19
**Build:** `database` v0.2.13, commit `1b8c3b1`, darwin/arm64
**Surface driven:** MySQL classic wire protocol and the operator CLI, through the project-local `verify-database` skill. No `internal/` package was used to obtain a result; source was read only to explain an already-observed failure.
**Instances:** one disposable run, `1789778193-88726`, with a generated password and its own data directory.
**Evidence:** `/tmp/verify-database-evidence/<run>/` (disposable; the file names referenced below identify the capture within a run).

This is an exploratory pass, not a conformance run. It attempted user journeys and followed surprises; it does not claim coverage of the v0.1 contracts.

## Result

Two defects confirmed in the product. Both are filed:

| # | Issue | Severity | Area |
| --- | --- | --- | --- |
| 1 | [#417](https://github.com/jonbaldie/database/issues/417) | Major | `CREATE UNIQUE INDEX` does not validate uniqueness against existing rows |
| 2 | [#418](https://github.com/jonbaldie/database/issues/418) | Major | Index hints with `FOR GROUP BY` scope fail with Error 1064 (requires a key list) |

Twenty-five relational set operation rules, subquery semantics, transaction rollback/savepoint hierarchies, row locks (`NOWAIT`, `SKIP LOCKED`), visibility transitions, functional indexes, and `AUTO_INCREMENT` lifecycle behaviors were exercised and **rejected as defects** — they behave as documented. Details under [Rejected with evidence](#rejected-with-evidence).

## Journeys attempted

### A. Relational set operations, subqueries, and common table expressions

**Goal:** compose queries using `UNION [ALL|DISTINCT]`, `INTERSECT [ALL|DISTINCT]`, `EXCEPT [ALL|DISTINCT]`, correlated and scalar subqueries, and non-recursive `WITH` common table expressions, verifying precedence, duplicate retention, `NULL` equality, and strict conversion semantics.

Ordinary path and variations succeeded:
- `UNION`, `INTERSECT`, and `EXCEPT` distinguish or retain duplicates correctly.
- Precedence holds: `INTERSECT` binds more tightly than `UNION` and `EXCEPT`, and parentheses override default precedence.
- Multiset difference (`EXCEPT ALL`) and multiset intersection (`INTERSECT ALL`) correctly decrement and bound duplicate counts.
- Scalar subqueries returning 0 rows yield `NULL`; subqueries returning >1 row fail with `1242`, and subqueries returning >1 column fail with `1241`.
- Non-recursive CTEs support chaining, referencing earlier CTEs, multiple consumers, and joining with base tables. Forward CTE references correctly fail with `1146`.
- Recursive CTEs (`WITH RECURSIVE`) and CTE column lists explicitly fail with `1235` (unsupported in v0.1).
- Unaliased derived tables fail with `1248`.
- Incompatible type unions (e.g. numeric with string) fail with `1292` requiring explicit cast.

### B. Transactions, savepoints, and isolation

**Goal:** execute multi-statement business transactions using `BEGIN`, `SAVEPOINT`, partial rollback (`ROLLBACK TO SAVEPOINT`), statement failure recovery, and isolation across concurrent sessions and server restarts.

Ordinary path and variations succeeded:
- `SAVEPOINT` outside an active transaction fails with `1196`.
- Overwriting an existing savepoint name discards the earlier savepoint and records the new state; rolling back to it restores to the second definition.
- Rolling back to an earlier savepoint restores state and destroys subsequent savepoints; attempting to roll back to a destroyed savepoint fails with `1305`.
- Statement errors inside a transaction (such as duplicate key `1062`) leave the transaction and its savepoints intact; subsequent valid operations commit successfully.
- Uncommitted transactions are invisible to other sessions (`REPEATABLE-READ`), and are rolled back cleanly across `control.sh restart`.
- Locking reads with `NOWAIT` immediately reject held rows with `3572`.
- Locking reads with `SKIP LOCKED` omit held rows and return unlocked rows.

### C. Schema evolution, indexes, and constraints

**Goal:** evolve table schemas by adding columns, default values, secondary, unique, descending, invisible, and functional indexes, and using index hints in queries.

This journey produced defects [#417](https://github.com/jonbaldie/database/issues/417) and [#418](https://github.com/jonbaldie/database/issues/418):
- `CREATE UNIQUE INDEX` and `ALTER TABLE ... ADD UNIQUE INDEX` fail to validate existing rows for duplicates, corrupting the table metadata.
- Queries using `USE INDEX FOR GROUP BY (...)`, `FORCE INDEX FOR GROUP BY (...)`, or `IGNORE INDEX FOR GROUP BY (...)` fail with `Error 1064 (42000): index hint requires a key list`.

Other behaviors in this journey behaved as documented:
- `ALTER TABLE ADD COLUMN` populates default values on existing rows.
- Unique indexes correctly allow multiple `NULL` values.
- `DROP INDEX` and `ALTER TABLE DROP INDEX` remove indexes cleanly.
- Descending indexes record collation `"D"` in `SHOW INDEX`.
- Invisible indexes record `"NO"` in `SHOW INDEX`, toggle to `"YES"` via `ALTER INDEX ... VISIBLE`, and `ALTER INDEX PRIMARY INVISIBLE` is refused with `3522`.
- Functional indexes (`((LOWER(name)))`) record expression and evaluate correctly during lookups.
- `AUTO_INCREMENT` sequence rules (advancement, non-reuse on `DELETE`, and reset on `TRUNCATE TABLE`) behave as documented.

---

## Confirmed defect 1 — `CREATE UNIQUE INDEX` ignores duplicates on existing rows (#417)

**User impact.** An operator can create a unique index on a table column or functional expression that already holds duplicate values. The database marks the index unique (`Non_unique: 0`), leaving duplicate rows under an index that promises uniqueness. Subsequent writes and DDL changes then fail unexpectedly due to internal duplicate key errors.

**Starting conditions.** Fresh instance, run `1789778193-88726`.

**Replay steps.**

```sql
CREATE DATABASE repro_defect1;
USE repro_defect1;
CREATE TABLE t_dup (id INT PRIMARY KEY, val INT);
INSERT INTO t_dup VALUES (1, 10), (2, 10);
CREATE UNIQUE INDEX idx_val ON t_dup (val);
SHOW INDEX FROM t_dup;
```

**Expected outcome.**
Per `docs/mysql-sql-behaviour.md`:
"A new constraint checks all existing rows before the schema changes. If a check fails, the previous schema definition remains in use."
The command must fail with `Error 1062 (23000): Duplicate entry for key 'idx_val'` and `idx_val` must not be added to `t_dup`.

**Actual outcome.**
`CREATE UNIQUE INDEX` succeeds with `{"statement":"CREATE UNIQUE INDEX idx_val ON t_dup (val)","ok":true,"rows_affected":0}`.
`SHOW INDEX FROM t_dup` returns `idx_val` with `Non_unique: 0`.

**Root cause.**
In `internal/mysql/constraint_enforcement.go`, `validateTableConstraints()` calls `validateUniqueIndexesAgainstPrevious()`.
For each unique index, `uniqueIndexUnchanged(previousTable, table, index, columns)` checks:
```go
func uniqueIndexUnchanged(previous, table catalog.Table, index catalog.Index, columns map[string]int) bool {
	if len(previous.Rows) != len(table.Rows) {
		return false
	}
	for rowIndex := range table.Rows {
		if sameRowRef(previous.Rows[rowIndex], table.Rows[rowIndex]) {
			continue
		}
...
```
When an index is added to an existing table, the rows are unchanged and share the same slice pointers (`sameRowRef == true`). The function never verifies whether `index` existed in `previous.Indexes`. It returns `true`, and `validateUniqueIndexesAgainstPrevious()` skips validation against existing rows.

Evidence captured in `journey-3-schema-and-indexes/defect1-create-unique-index-duplicates.jsonl`.

---

## Confirmed defect 2 — Index hints with `FOR GROUP BY` scope fail with syntax error (#418)

**User impact.** Users cannot use `FOR GROUP BY` index hints (`USE INDEX FOR GROUP BY (...)`, `FORCE INDEX FOR GROUP BY (...)`, `IGNORE INDEX FOR GROUP BY (...)`). Any query using this documented hint syntax fails with a syntax error.

**Starting conditions.** Fresh instance, run `1789778193-88726`.

**Replay steps.**

```sql
CREATE DATABASE repro_defect2;
USE repro_defect2;
CREATE TABLE t_hint (id INT PRIMARY KEY, a INT, KEY idx_a (a));
INSERT INTO t_hint VALUES (1, 10), (2, 20);

SELECT a FROM t_hint USE INDEX FOR JOIN (idx_a);       -- succeeds
SELECT a FROM t_hint USE INDEX FOR ORDER BY (idx_a);   -- succeeds
SELECT a FROM t_hint USE INDEX FOR GROUP BY (idx_a);   -- fails: Error 1064
```

**Expected outcome.**
Per `docs/mysql-sql-behaviour.md`:
"`USE INDEX`, `FORCE INDEX`, and `IGNORE INDEX` support `FOR JOIN`, `FOR ORDER BY`, and `FOR GROUP BY` scopes."
The query parses and executes successfully with the index hint applied to the `GROUP BY` scope.

**Actual outcome.**
`SELECT a FROM t_hint USE INDEX FOR GROUP BY (idx_a)` returns:
`{"statement":"SELECT a FROM t_hint USE INDEX FOR GROUP BY (idx_a)","ok":false,"rows_affected":0,"error":"Error 1064 (42000): index hint requires a key list","error_code":1064}`.

**Root cause.**
In `internal/mysql/relational_select.go`, `splitSelectTail()` identifies clause boundaries using `selectClauseAt()`:
```go
func selectClauseAt(text, keyword string) int {
	start := 0
	length := len(text)
	for start < length {
		at := keywordAt(text[start:], keyword)
		if at < 0 {
			return -1
		}
		at += start
		if keyword != "order" || !indexHintOrderKeyword(text, at) {
			return at
		}
		start = at + len(keyword)
	}
	return -1
}
```
`selectClauseAt()` exempts `"order"` when preceded by `"for"` so that `FOR ORDER BY` in an index hint is not mistaken for the query's `ORDER BY` clause.
However, it does not exempt `"group"`. When `selectClauseAt(tail, "group")` runs, it matches `"group"` inside `FOR GROUP BY` and treats it as the beginning of the query's `GROUP BY` clause. This truncates the `FROM` clause right after `FOR `, passing an incomplete index hint string to `parseRelationalIndexHints()`.

Evidence captured in `journey-3-schema-and-indexes/defect2-use-index-for-group-by.jsonl`.

---

## Rejected with evidence

The following candidate behaviors were tested and confirmed to comply with the documented contracts:

1. **Set operation precedence:** `INTERSECT` binds tighter than `UNION` and `EXCEPT`. `SELECT 1 UNION SELECT 2 INTERSECT SELECT 2` yields `[1, 2]`; `(SELECT 1 UNION SELECT 2) INTERSECT SELECT 2` yields `[2]`.
2. **Multiset arithmetic:** `EXCEPT ALL` and `INTERSECT ALL` correctly track row duplicate multiplicity.
3. **Set operations with NULLs:** Distinct set comparison treats `NULL` values as equal.
4. **Collation and type reconciliation in UNION:** Unifying incompatible types (numeric and string) correctly requires explicit cast (`1292`); compatible numerics (`INT` and `DECIMAL`) promote losslessly.
5. **Scalar subquery cardinailty:** 0 rows yields `NULL`; >1 row produces `1242`; >1 column produces `1241`.
6. **CTE dependency handling:** Non-recursive CTEs chain and evaluate in declaration order; forward references fail with `1146`.
7. **Unsupported CTE forms:** `WITH RECURSIVE` and CTE column lists explicitly reject with `1235`.
8. **Savepoint outside transaction:** Refused with `1196`.
9. **Savepoint hierarchy:** Rolling back to an earlier savepoint keeps it and destroys subsequent savepoints. Subsequent rollback to a destroyed savepoint fails with `1305`.
10. **Savepoint overwrite and release:** Overwriting a savepoint updates its snapshot; `RELEASE SAVEPOINT` removes it cleanly.
11. **Transaction resilience to statement errors:** A failed statement (e.g. duplicate key `1062`) does not roll back the active transaction or invalidate open savepoints; subsequent statements commit successfully.
12. **Restart durability and rollback:** Committed transactions survive `control.sh restart`; uncommitted transactions are rolled back.
13. **Row lock contention:** `FOR UPDATE NOWAIT` and `FOR SHARE NOWAIT` fail immediately with `3572` against held rows. `SKIP LOCKED` skips held rows without waiting.
14. **Index visibility:** Invisible indexes record `"NO"` in `SHOW INDEX`, toggle to `"YES"`, and primary key cannot be made invisible (`3522`).
15. **Functional indexes:** Expression indexes evaluate accurately and appear with expression text in `SHOW INDEX`.
16. **AUTO_INCREMENT sequences:** Sequence jumps on explicit high values, ignores reused values after `DELETE`, and resets to 1 on `TRUNCATE TABLE`.
