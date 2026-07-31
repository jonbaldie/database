# v0.1 MySQL SQL behaviour contract

This is the normative SQL behaviour contract for the documented v0.1 SQL
surface. It is deliberately finite: it does not make database a complete MySQL
implementation or a drop-in MySQL replacement.

## Baseline and scope

For every supported SQL construct, database follows the observable results,
value types, metadata, warnings, errors, and session effects of
[MySQL 8.4.11](https://dev.mysql.com/doc/relnotes/mysql/8.4/en/news-8-4-11.html),
unless this document or another v0.1 contract explicitly declares a deviation.
An unlisted difference inside the supported surface is a defect. A later MySQL
8.4 patch affects this baseline only through a documented database release.

This rule does not support syntax or features merely because MySQL supports
them. The documented v0.1 relational, session, account, catalog, and protocol
contracts define what is supported. A more specific v0.1 contract takes
precedence where it intentionally differs from this one.

## Transactions, savepoints, and failure boundaries

The server supports `BEGIN`, `START TRANSACTION`, `COMMIT [WORK]`, `ROLLBACK
[WORK]`, `SAVEPOINT identifier`, `ROLLBACK [WORK] TO [SAVEPOINT] identifier`,
and `RELEASE SAVEPOINT identifier`. A savepoint needs an active transaction.
Its name is one SQL identifier and follows the identifier comparison rule.

`SAVEPOINT` records the current transaction state. A savepoint with an existing
name deletes the old savepoint, then records a new one at the current point.
`ROLLBACK TO` restores the named point without ending the transaction. It keeps
the named savepoint and deletes each savepoint recorded after it. `RELEASE
SAVEPOINT` deletes only its named savepoint. An unknown or released savepoint
fails with MySQL error `1305` and SQLSTATE `42000`. A full `COMMIT` or
`ROLLBACK` deletes all savepoints.

An ordinary failed statement, including a duplicate-key error or a lock-wait
timeout, has no partial effect and leaves an explicit transaction and its
available savepoints open. The application can correct the error and continue,
or roll back to a savepoint. A deadlock or another whole-transaction failure
discards all work since the transaction started. Its retryable identity is
MySQL error `1213` and SQLSTATE `40001`; the application must retry the whole
transaction. A lock-wait timeout uses MySQL error `1205` and SQLSTATE `HY000`.

### Concurrent row locks

`UPDATE` and `DELETE` take exclusive locks on each matched direct table row.
`SELECT ... FOR UPDATE` takes exclusive row locks and `SELECT ... FOR SHARE`
or `LOCK IN SHARE MODE` takes shared row locks. A locking read supports the
`NOWAIT` and `SKIP LOCKED` forms. A lock is kept until the full `COMMIT`,
`ROLLBACK`, connection cancellation, or deadlock rollback; `ROLLBACK TO
SAVEPOINT` does not release it. `INSERT`, `REPLACE`, and duplicate-key insert
updates retain their established optimistic conflict handling.

The lock scope is the returned rows of a direct table relational `SELECT`,
after `DISTINCT`, `ORDER BY`, and `LIMIT` shape its result. Joins lock the
returned rows from every direct source table. A conflicting ordinary lock
request waits up to `lock_wait_timeout_ms`. `NOWAIT` returns MySQL error
`3572` and SQLSTATE `HY000` without a statement effect. `SKIP LOCKED` omits
conflicting rows and takes locks only on rows that it returns.
Timeout and `NOWAIT` failures leave an explicit transaction open. A detected
wait cycle rolls back the request that completes the cycle, releases its locks,
and returns MySQL error `1213` and SQLSTATE `40001`. Connection cancellation
rolls back its active transaction and releases its locks before later work can
acquire them.

## Values, parameters, and predicates

Implicit conversion is permitted only when it is lossless and stays in a
compatible family: numeric widening, exact integer-to-`DECIMAL` promotion,
compatible character or binary widening, and `DATE` to `DATETIME`. Signed and
unsigned numeric conversion, exact and approximate numeric conversion,
character-to-numeric, character-to-temporal, and character-to-binary conversion
require an explicit cast.

An explicit `CAST` follows MySQL for the representation change deliberately
requested, including documented rounding or truncation. A malformed or
out-of-range cast fails. A prepared parameter may be parsed as its unambiguous
expected SQL type only when its supplied representation is canonical and the
conversion is exact. Ambiguous, malformed, or lossy parameter conversion fails.
Except for that driver-facing contextual conversion, prepared and text execution
have the same logical behaviour and metadata.

`BOOLEAN` and `TINYINT(1)` predicates retain MySQL truth semantics: zero is
false, nonzero is true, and `NULL` is unknown. Character, binary, and temporal
values require an explicit cast before use as predicates.

## Arithmetic, comparison, collation, and NULL

MySQL result-type and numeric-promotion rules apply. Division by zero,
overflow, invalid function domains, and non-finite `FLOAT` or `DOUBLE` inputs or
results are errors; values do not saturate or wrap. Direct comparison requires
compatible families and a lossless common numeric type where one exists.
Character-to-numeric and character-to-temporal comparison require explicit
conversion. MySQL null-safe equality (`<=>`) is supported.

`utf8mb4_0900_ai_ci` and `utf8mb4_bin` follow MySQL 8.4.11 comparison,
trailing-space, coercibility, and `LIKE` behaviour. Mixed collations use MySQL
coercibility rules and fail when there is no unambiguous result. Invalid UTF-8
always fails. An assignment exceeding its declared character or binary length
fails atomically: v0.1 never silently truncates, replaces invalid characters,
discards significant padding, or reinterprets binary content as text. Ordinary
declared padding and comparison behaviour otherwise follows the baseline.

`NULL` follows MySQL three-valued logic, aggregate behaviour, ordering,
uniqueness, nullable foreign-key behaviour, and check-constraint behaviour.
In particular, multiple `NULL`-containing keys may exist under `UNIQUE`, and a
`CHECK` result that is true or unknown is accepted. Without `ORDER BY`, row
order is unspecified, including with `LIMIT`; reproducible plan choice does not
promise row-order stability.

### Constraints

`CREATE TABLE` supports `NOT NULL`, literal `DEFAULT`, `PRIMARY KEY`, `UNIQUE`,
`FOREIGN KEY ... REFERENCES`, and `CHECK` constraints. Constraints can be named
with `CONSTRAINT name`. `ALTER TABLE ... ADD CONSTRAINT` supports the same table
constraints. A new constraint checks all existing rows before the schema
changes. If a check fails, the previous schema definition remains in use.

Each insert, update, delete, and schema change checks the complete affected
constraint surface before it becomes durable. Foreign keys require a primary or
unique referenced key. No foreign-key action clause is supported. A write that
would remove or change a referenced value fails. `SHOW CREATE TABLE` shows the
saved column rules and constraints.

## Mutation forms

`INSERT` supports `INSERT INTO table [(column, ...)] VALUES (...) [, ...]`,
the singular `VALUE` spelling, `INSERT INTO table SET column = value [, ...]`,
and `INSERT INTO table [(column, ...)] SELECT ... FROM ...`. The select source
uses the supported direct relational `SELECT` form. The server materializes its
source rows from the statement snapshot before it changes the target; this also
makes a same-table insert-select finite and deterministic.

`REPLACE [INTO]` supports the same `VALUES`, `VALUE`, `SET`, and direct
`SELECT` source forms. For each submitted row, it removes every stored row that
conflicts on a primary key, a `UNIQUE` constraint, or a unique index, then
inserts the submitted row. Its affected-row count is one for the inserted row
plus one for each removed row.

`INSERT` sources can end with `ON DUPLICATE KEY UPDATE`. An inserted row counts
as one affected row. A conflicting row is updated once; a changed update counts
as two affected rows and an unchanged update counts as zero. Each update right
side is either a scalar literal or `VALUES(column)`, which means the submitted
candidate value. The server does not negotiate `CLIENT_FOUND_ROWS`, so an
unchanged update remains zero through both text and prepared execution.

The server canonicalizes all submitted rows and then validates the final table
and database namespace state before it publishes the statement. A type error,
duplicate, check, not-null, or foreign-key error has no partial effect. This
rule applies to multi-row values, select sources, replacement deletes, and
duplicate-key updates. There are no trigger effects in v0.1.

`INSERT IGNORE`, priority modifiers, `DELAYED`, `RETURNING`, row constructors,
`REPLACE ... TABLE`, insert-source aliases, and duplicate-key update
expressions other than scalar literals or `VALUES(column)` are outside v0.1.
They fail before durable effects.

## Composed relational queries

The v0.1 relational surface supports scalar subqueries in projections,
`EXISTS` and single-column `[NOT] IN` predicates, derived tables with required
aliases, and non-recursive `WITH` common table expressions. A subquery may
reference columns from any enclosing query scope; its own columns shadow outer
columns. A scalar subquery must return one column and at most one row: no row
produces `NULL`, while extra columns or rows fail with MySQL error 1241 or 1242.
Derived tables and CTEs expose their projected names, types, collations, and
nullability to their consumers. Recursive CTEs and CTE column lists are outside
v0.1.

`UNION`, `INTERSECT`, and `EXCEPT`, with optional `ALL` or `DISTINCT`, are
supported. `INTERSECT` binds more tightly than `UNION` and `EXCEPT`; parentheses
override that precedence. Distinct set comparison treats `NULL` values as equal
and compares character values through the reconciled collation. `ALL` preserves
duplicate multiplicity. Inputs must have equal arity and a lossless common type
under this contract's conversion rules. Result names come from the first term;
types, lengths, collations, and nullability are reconciled across every term.
A whole-expression `ORDER BY` may use a first-term name or ordinal, and is
applied before a whole-expression `LIMIT`; without it, result order is
unspecified.

## Temporal values

`TIMESTAMP` is an instant rendered through the session time zone; `DATETIME` is
timezone-naive. The session-settings contract defines the supported time-zone
values. Fractional temporal precision is zero through six. Zero dates or years,
two-digit-year expansion, invalid calendar values, excess fractional precision,
and out-of-range temporal arithmetic fail rather than rounding or normalizing.
Current-time functions are stable within one statement. Other temporal results,
`NULL` behaviour, and metadata follow the baseline subject to these deviations.

## Functions, warnings, and errors

The closed v0.1 function registry follows MySQL argument, `NULL` propagation,
return-type, collation, and metadata behaviour, subject to this contract's
strict conversion and arithmetic rules. Undocumented aliases are unsupported.

Any MySQL warning for truncation, invalid encoding, invalid temporal data,
overflow, division by zero, or lossy implicit conversion is a statement error.
A write then has no partial durable effects. Benign MySQL notes and warnings
remain observable through the warning count and `SHOW WARNINGS`.

Equivalent failures use MySQL 8.4.11's numeric error and SQLSTATE. An
intentional v0.1 deviation uses the closest applicable MySQL identity, recorded
in the relevant compatibility matrix. Numeric identity and SQLSTATE are stable;
message wording and diagnostic ordering are not stable unless statement outcome
depends on them.

## Fixed public ceilings

These ceilings are product behaviour, not storage-engine limits. Exceeding one
fails explicitly before durable effects:

| Subject | v0.1 ceiling |
| --- | --- |
| `DECIMAL` | precision 1–65; scale 0–30 and no greater than precision |
| `BIT` | width 1–64 |
| Character or binary scalar | 16 MiB |
| Logical row | 64 MiB across its column values |
| Columns per table | 1,024 |
| Indexes per table | 64, including the primary index |
| Columns or expressions per index | 16 |
| Declared encoded index-key width | 3,072 bytes |
| Tables referenced by one statement | 64 |
| Expression, subquery, or CTE nesting on one path | 64 levels |
| Parameter markers in one prepared statement | 65,535 |

An index definition that can exceed the key-width ceiling is rejected; a prefix
index may bring a key within the ceiling. Integer and temporal ranges otherwise
follow the baseline. Packet, execution-resource, storage-capacity, session, and
prepared-statement-count limits are governed by their own public contracts.
This contract does not prescribe value representations, arithmetic libraries,
parser organization, collation machinery, or storage layout.

## B-tree indexes

The server supports B-tree primary, unique, secondary, composite, prefix, and
functional indexes. A key part is ascending by default and can be descending.
Indexes are visible by default; an invisible secondary index is maintained but
is not chosen automatically. A primary index cannot be invisible.

`CREATE INDEX`, `CREATE UNIQUE INDEX`, `DROP INDEX`, and the corresponding
`ALTER TABLE` index actions are supported. `SHOW INDEX` and `SHOW CREATE TABLE`
expose the durable index definition. A unique index rejects duplicate non-NULL
key values. Functional and prefix key values use the same rule.

`USE INDEX`, `FORCE INDEX`, and `IGNORE INDEX` support `FOR JOIN`, `FOR ORDER
BY`, and `FOR GROUP BY` scopes. A hint can name an unambiguous index-name
prefix. Unknown, ambiguous, invisible, or otherwise invalid hint names fail
explicitly. A valid hint limits or requires the logical access-path selection;
it does not override semantic query checks.
