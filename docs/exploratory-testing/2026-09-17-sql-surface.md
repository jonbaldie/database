# Exploratory testing pass: authorization, catalog, and query explanation

**Date:** 2026-09-17
**Build:** `database` v0.2.7, commit `8dfa982`, darwin/arm64
**Surface driven:** MySQL classic wire protocol and the operator CLI, through the
project-local `verify-database` skill. No `internal/` package was used to obtain
a result; source was read only to explain an already-observed failure.
**Instances:** two disposable runs, `1789673087-63001` and `1789673356-69618`,
each with a generated password and its own data directory.
**Evidence:** `/tmp/verify-database-evidence/<run>/` (disposable; the file names
referenced below identify the capture within a run).

This is an exploratory pass, not a conformance run. It attempted user journeys
and followed surprises; it does not claim coverage of the v0.1 contracts.

## Result

Six defects confirmed, five in the product and one in the verification harness.
All are filed:

| # | Issue | Severity | Area |
| --- | --- | --- | --- |
| 1 | [#399](https://github.com/jonbaldie/database/issues/399) | Critical | Data authorization uses the session default namespace |
| 2 | [#400](https://github.com/jonbaldie/database/issues/400) | Major | Documented catalog `SHOW` surface largely unimplemented |
| 3 | [#401](https://github.com/jonbaldie/database/issues/401) | Major | `EXPLAIN ANALYZE FORMAT=JSON` emits `null` for required arrays |
| 4 | [#402](https://github.com/jonbaldie/database/issues/402) | Major | Ordering `direction` emitted outside the schema enum |
| 5 | [#403](https://github.com/jonbaldie/database/issues/403) | Minor | `SET` misreports a double-quoted value as a bad value |
| 6 | [#404](https://github.com/jonbaldie/database/issues/404) | Minor | `verify-database` harness always reports `rows_affected: 0` |

Twenty-two account-administration boundaries, the affected-row contract, the
constraint and strict-conversion rules, set-operation precedence, the session
settings registry, and every plan-mode explain semantic invariant were exercised
and **rejected as defects** — they behave as documented. Details under
[Rejected with evidence](#rejected-with-evidence).

## Journeys attempted

### A. An application developer builds a shop schema and trusts it

**Goal:** create a namespace and tables, insert data, and have constraints and
mutation counts behave as `docs/mysql-sql-behaviour.md` promises.

Ordinary path succeeded. Variations attempted: violating each constraint class,
submitting values needing implicit conversion, and re-reading after a restart.

Foreign keys rejected with `1452` on insert and `1451` on delete, `CHECK`
rejected with `3819`, and in every case the table was left unchanged — matching
the contract's "the server canonicalizes all submitted rows and then validates
the final table and database namespace state before it publishes the statement".
Strict conversion produced `1292`, division by zero `1365`, overflow `1690`, and
an unregistered function `1305`. Writes survived `control.sh restart` and were
visible in the on-disk `catalog.json`.

One surprise here turned out to be an observation defect rather than a product
defect, and is recorded as [#404](https://github.com/jonbaldie/database/issues/404).
See [Observation repair](#observation-repair).

### B. A multi-account application with a reporting and writer boundary

**Goal:** give a writer account `DATA_READ, DATA_WRITE` on `shop.*` and a reader
account `DATA_READ` on `shop.*`, and have each confined to that namespace and to
those verbs, per `docs/account-administration.md`.

This journey produced defect [#399](https://github.com/jonbaldie/database/issues/399).
The boundary does not hold. It is the most serious finding of the pass, so it is
written out in full below.

### C. A developer inspects queries with the query-explanation feature

**Goal:** run `EXPLAIN` and `EXPLAIN ANALYZE` with `FORMAT=JSON` and get
documents that satisfy the published `database.explain/v1` contract and its
shipped JSON Schema.

A 17-document corpus was generated across reads, writes, CTEs, and set
operations. The semantic invariants in `docs/query-explanation/README.md` hold:
plan documents omit `actual` at every node, analyze documents include it at every
node, operator IDs are unique positive integers, the operator vocabulary stays
inside the closed 16-kind list, an unused CTE has no operator, a referenced CTE
appears once as `materialize` with reason `cte` and later as `reuse`, ODKU uses
mutation type `upsert`, `REPLACE` exposes its delete-and-insert pair,
`INSERT ... SELECT` exposes its read tree, and `EXPLAIN ANALYZE` on a locking
read is refused with `1235` rather than returning a partial document. The
forbidden `value`, `bound_value`, `parameter_value`, and `raw_value` fields were
absent, and no `-1` or `unknown` sentinels appeared.

The documents nonetheless fail the product's own shipped schema, for two
independent reasons: [#401](https://github.com/jonbaldie/database/issues/401) and
[#402](https://github.com/jonbaldie/database/issues/402).

## Confirmed defect 1 — authorization uses the wrong namespace (#399)

**User impact.** An account with no grants at all can read any table in any
namespace. The disclosure needs no special client: the quick-start DSN in
`README.md`, `admin:...@tcp(127.0.0.1:3306)/`, selects no default namespace, so
the documented ordinary client configuration is the vulnerable one.

**Starting conditions.** Fresh instance, run `1789673356-69618`.

**Replay steps.** As `admin`:

```sql
CREATE DATABASE secrets;
USE secrets;
CREATE TABLE salaries (id INT PRIMARY KEY, name VARCHAR(40) NOT NULL, salary INT NOT NULL);
INSERT INTO salaries VALUES (1,'Ana',95000),(2,'Ben',82000);
CREATE USER 'nobody1' IDENTIFIED BY 'nobody-password-123';
SHOW GRANTS FOR 'nobody1';
```

`SHOW GRANTS` returns zero rows, confirming the account holds nothing. Then
connect as `nobody1` **with no default namespace** and run one statement:

```sql
SELECT id, name, salary FROM secrets.salaries ORDER BY id;
```

**Expected.** `1227 access denied`, since "No grant implies another" and the
catalog contract states "It never exposes row data".

**Actual.** The rows are returned:

```json
{"ok":true,"columns":["id","name","salary"],"rows":[["1","Ana","95000"],["2","Ben","82000"]]}
```

**Repeat observations.** Reproduced on both runs, and replayed from a
brand-new instance as the first statements executed on it. The in-product control
is decisive: after `USE secrets`, the identical statement on the identical
session is correctly denied `1227`. Evidence `repro-1-setup.jsonl`,
`repro-2-disclosure.jsonl`, `repro-3-control.jsonl`.

**Cross-namespace write facet.** `shopwriter`, holding only `DATA_READ` and
`DATA_WRITE` on `shop.*`, with `USE shop` active, successfully ran `SELECT`,
`INSERT INTO rowcount.t VALUES (30,'pwn',30)`, `UPDATE rowcount.t SET qty=1234`,
and `DELETE` against namespace `rowcount`, where it holds nothing. The effects
survived `control.sh restart`, so this is durable committed data. Evidence
`C2-cross-namespace-attempt.jsonl`, `C3-damage-confirmed.jsonl`,
`C6-durable-after-restart.jsonl`.

**Direction of the error.** The check reads the wrong namespace rather than
failing open unconditionally. `shopwriter` with `USE rowcount` is correctly
denied, and `shopreader` with `USE shop` can wrongly read `rowcount.t` but is
denied the insert — denied for lacking `DATA_WRITE` on *shop*, not on
*rowcount*. Authorized work is also falsely denied: a qualified write with no
default namespace selected returns `1227`.

**Root cause.** `internal/mysql/account_administration.go`, `statementGrant`,
authorizes against `s.session.database` and never parses the namespace the
statement references. The disclosure is the `namespace == ""` branch of
`readGrant`, which returns an empty privilege string, so `requireGrant` is never
called and no authorization happens at all.

## Confirmed defect 2 — catalog SHOW surface largely unimplemented (#400)

**User impact.** `DESCRIBE <table>` and `SHOW COLUMNS` are the ordinary way to
inspect a table and are issued automatically by much MySQL client tooling. Both
fail with `1064 unsupported query` as `admin` holding every grant, while
`docs/catalog-metadata.md` lists them in a closed supported surface.

Failing as `1064`: `SHOW COLUMNS FROM t` in unqualified, qualified, backticked,
`IN`, and `LIKE` forms; `SHOW FULL COLUMNS`; `DESCRIBE t`; `DESC t`;
`DESCRIBE db.t`; `SHOW TABLES FROM db`; `SHOW TABLES IN db`; `SHOW FULL TABLES`;
`SHOW FULL TABLES FROM db`; `SHOW TABLES WHERE ...`.

Working: `SHOW TABLES`, `SHOW TABLES LIKE`, `SHOW DATABASES [LIKE]`,
`SHOW INDEX FROM [db.]tbl`, `SHOW CREATE TABLE`.

A separate parsing fault affects a form that otherwise works:
`SHOW INDEX FROM salaries FROM secrets` fails
`1146 table 'secrets.salaries FROM secrets' doesn't exist` — the second `FROM`
clause is swallowed into the table identifier.

Root cause is the `showCatalog` dispatch in `internal/mysql/server.go`, which has
no case for these forms and matches `show tables` only as an exact string or a
`like` prefix. Evidence `D5-show-from-forms.jsonl`, `D6-describe-confirm.jsonl`.

## Confirmed defects 3 and 4 — explanation documents fail their own schema (#401, #402)

Both were found by validating live server output against the shipped
`docs/query-explanation/explain-v1.schema.json` with the repository's own pinned
tooling (`ajv-cli@5.0.0`, `ajv-formats@3.0.1`, `--spec=draft2020`).

**#401, analyze mode emits `null` for required arrays.** Minimal reproducer, no
schema required:

```sql
EXPLAIN FORMAT=JSON SELECT 1;          -- warnings: [], statement.parameters: []
EXPLAIN ANALYZE FORMAT=JSON SELECT 1;  -- warnings: null, statement.parameters: null
```

ajv calls the first document `valid` and the second `invalid`. The affected
fields are the document `warnings`, `statement.parameters`, every operator's
`warnings`, and every `actual.warnings` — all `required` arrays. This contradicts
"`warnings`: document-wide structured warnings, always an array" and "Required
fields remain present, including meaningful zeroes and empty arrays".
`actual.first_row_ms: null` is **not** a defect; the schema types it
`nullableNonNegativeNumber` and the README permits first-row time to be null.
Across the corpus, both analyze documents carried these nulls (15 and 17
offending paths) and all fifteen plan documents carried none.

**#402, ordering direction is outside the schema enum.** The server emits
`"direction": "ASC"` and `"DESC"`; the schema permits only `["asc","desc"]`, and
the shipped `examples/plan.json` uses lower-case `"desc"`. This affects plan mode
as well, so it is independent of #401. Three corpus documents reported an
ordering guarantee and all three were upper case.

**Why the gate missed both.** `scripts/validate-query-explanation-schema.sh`
validates only the four hand-written examples, so
`make validate-query-explanation` never checks a document the server produced.
A plain `ORDER BY` and a bare `EXPLAIN ANALYZE ... SELECT 1` are enough to trip
both defects, so validating live output would close this class.

Evidence `E1-explain-analyze-null-arrays.txt`, `E2-ajv-schema-verdicts.txt`,
`E8-explain-defect-classification.txt`.

## Confirmed defect 5 — SET misreports a double-quoted value (#403)

`SET time_zone = "UTC"` is rejected as
`1298 Unknown or unsupported time zone: '"UTC"'`, although
`docs/session-settings.md` lists `UTC` as supported. The quote characters are
folded into the value, and the message names a value the user never submitted.
`SET collation_connection = "utf8mb4_bin"` behaves the same way.

The cause is that double-quoted string literals are unsupported. In an
expression the server says so honestly (`SELECT "x"` gives `1064 unsupported
expression`), so one token is handled two different ways, and only the `SET`
path misattributes the cause to the value.

Filed as the inconsistency and the misleading diagnostic, **not** as the absence
of double-quoted literals: `docs/mysql-sql-behaviour.md` states the baseline rule
"does not support syntax or features merely because MySQL supports them", so the
scope choice is defensible. No contract states which quoting forms are supported
(`grep -rniE 'ansi_quotes|double.quot' docs/` returns nothing), which is a
documentation gap given that the declared MySQL 8.4.11 baseline treats `"UTC"` as
a string literal under the server's own fixed `sql_mode`.

Evidence `E5-session-settings.jsonl`, `E6-double-quote-literal.jsonl`,
`E7-double-quote-inconsistency.jsonl`.

## Observation repair

`control.sh sql` reported `rows_affected: 0` for a three-row `INSERT`. Read as
product output, that is an affected-row defect. It is not.
`.agents/skills/verify-database/helpers/sqlclient/main.go` routes every statement
through `pool.Query` and never calls `Result.RowsAffected()`, while declaring the
field without `omitempty`, so it always serializes as `0`.

Because the contract makes specific affected-row promises, observation had to be
repaired before any conclusion. An `Exec`-based client was written outside the
repository and the contract replayed from a clean namespace. Every promise holds:
a two-row insert counts 2; ODKU counts 0 unchanged, 2 changed, and 1 inserted.
The candidate is therefore **rejected**, and the harness gap is filed as
[#404](https://github.com/jonbaldie/database/issues/404).

Disclosure: that replacement client is an intervention. The original zeros are
retained in `A3-affected-rows.jsonl`; the repaired measurements are in
`E3-odku-affected-rows.jsonl`. The intervention changed only the observing
client, not the server or its configuration.

## Rejected with evidence

These were probed as potential defects and behave as documented.

- **Account administration, 22 boundaries.** Password 12-byte minimum; empty
  password; empty, `_leading`, `has space`, and 33-character names rejected;
  32-character name accepted; duplicate account `1396`; `IF NOT EXISTS` and
  `IF EXISTS` no-ops with Note 3163; revoking an absent grant `1141`; idempotent
  grant; grant on an unknown namespace `1049`; `GRANT SUPER`, wrong-scope, and
  `*.*` data grants `1064`; all three last-manager protections `1396`.
- **Account visibility.** `information_schema.ACCOUNTS` and `ACCOUNT_GRANTS` row
  filtering, and `SHOW GRANTS FOR` authorization.
- **Catalog namespace visibility.** A zero-grant account sees no namespace
  through `SHOW DATABASES`, `SCHEMATA`, `TABLES`, or `COLUMNS`, and gets `1044`
  from `SHOW CREATE TABLE`, `SHOW INDEX`, and `SHOW CREATE DATABASE`. Note that
  the metadata boundary holds while the row-data boundary of #399 does not.
- **Affected rows.** Every count correct once observation was repaired.
- **Constraints.** `1451`, `1452`, `3819`, with no partial effect.
- **Strict typing.** `1292`, `1365`, `1690`, and closed function registry `1305`.
- **Set operations.** `INTERSECT` binds tighter than `UNION` and `EXCEPT`.
- **Session settings.** Fixed `sql_mode`; documented defaults; `sql_mode`
  read-only `1238`; out-of-range `statement_timeout_ms` `1231`; invalid value
  `1231`; unknown variable `1193`; `SET GLOBAL` unsupported `1231`; unsupported
  collation `1273`; `SET name = DEFAULT` restores the server value.
- **Explain semantics.** All plan-mode invariants listed under journey C.

## Unresolved

Not classified, because the supported surface does not settle them.

- `DEFAULT 0x4742` and `DEFAULT (0x4742)` rejected `1064 DEFAULT requires a
  literal value`. A hex literal is a literal in MySQL, but v0.1 hex-literal
  support is unconfirmed.
- `CAST('2020-02-30' AS DATE)` gives `1064 unsupported expression` rather than a
  value error. `DATE` as a cast target may simply be out of scope.
- `DATE '0000-00-00'` gives `1054 Unknown column 'DATE'`, suggesting typed
  temporal literal syntax is unsupported rather than mis-evaluated.

## Not explored

Recorded so later work knows what this pass did not touch: transactions and
savepoints, including the savepoint error `1305` and the lock codes `1205`,
`1213`, and `3572`; the fixed public ceilings table; the diagnostics endpoints
`/live`, `/ready`, and `/metrics` beyond the `doctor` check; prepared-statement
and protocol-level behaviour; backup, restore, and upgrade; and concurrency of
any kind. Every finding here is single-session.

## Usability observations

Observations, kept separate from the suggestions that follow them.

- The quick-start DSN in `README.md` selects no default namespace, which is the
  exact configuration in which #399 discloses data. A reader following the
  README lands on the unsafe path by default.
- `1064 unsupported query` is returned both for syntax outside v0.1 and for
  documented-but-unimplemented forms such as `DESCRIBE`. A user cannot tell
  "never supported" from "listed as supported but missing".
- The `1298` and `1273` messages in #403 quote a value the user did not submit.
  The added quote characters are easy to overlook in terminal output.
- `rows_affected: 0` is indistinguishable from "not measured" (#404).

Suggestions, which follow from the above but are not findings: give the README
quick start a default namespace, or document why one is needed; separate the
error identity for unimplemented-but-documented forms from genuinely
unsupported syntax; and extend `make validate-query-explanation` to validate
live server output, since two statements would have caught #401 and #402.

## Limitations of this pass

Single-node, single-session, loopback only, on one platform (darwin/arm64) and
one build. The `rows_affected` measurements come from a replacement client, as
disclosed above. Source was read to explain observed failures and to locate root
causes; no finding rests on source reading alone, and every reproducer runs
through the MySQL wire.
