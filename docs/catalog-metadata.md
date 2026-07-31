# v0.1 catalog metadata contract

This document is the normative v0.1 contract for the
[catalog metadata surface](../CONTEXT.md). It defines what an authenticated
database account may discover through supported `SHOW` statements and the
always-visible [catalog namespace](../CONTEXT.md), and the MySQL-shaped shape
and meaning of those results. It is a finite compatibility surface, not an
open-ended system catalog.

The [MySQL SQL behaviour baseline](mysql-sql-behaviour.md) supplies the
MySQL 8.4.11 result, warning, error, and session rules unless this document
explicitly narrows or changes them. The contract fixes observable product
behaviour only. Namespace/catalog storage, catalog-row construction,
planning-statistics maintenance, canonical-SQL rendering, and
authorization-filtering algorithms remain implementation choices.

## Visibility and namespace boundaries

`information_schema` is an always-visible, read-only virtual namespace. Every
authenticated database account may select from it and use its supported
metadata statements.

A [database namespace](../CONTEXT.md) is visible when the current account holds
any namespace-scoped grant on it: `DATA_READ`, `DATA_WRITE`, or
`SCHEMA_MANAGEMENT`. One of these grants exposes the complete relational
definition of every table, column, index, and constraint in that namespace.
It never exposes row data and never changes authorization for ordinary data
queries.

`NAMESPACE_MANAGER` additionally exposes the names of every persistent
namespace, because it authorizes namespace creation and removal. By itself it
does not expose any object definition or row. `ACCOUNT_MANAGER`,
`OPERATIONAL_OBSERVATION`, and `OPERATIONAL_CONTROL` do not widen catalog
visibility; the settled row filtering for account, grant, and process
information remains in force.

`SHOW DATABASES` and `information_schema.SCHEMATA` expose the same
account-specific set of persistent namespace names, together with the
always-visible `information_schema` namespace. Enumeration omits invisible
namespaces and all of their contents.

The names `information_schema`, `mysql`, `performance_schema`, and `sys` are
reserved. Only `information_schema` exists in v0.1. Creating or using any of
the other three fails explicitly; an account cannot create, drop, or mutate
`information_schema`.

## Supported `information_schema` views

The standard catalog views are a closed set:

| View | Meaning |
| --- | --- |
| `SCHEMATA` | Visible persistent namespaces plus `information_schema`. |
| `TABLES` | Visible persistent tables and the supported catalog views. |
| `COLUMNS` | Columns of visible persistent tables and supported catalog views. |
| `STATISTICS` | Indexes and index parts for visible persistent tables. |
| `TABLE_CONSTRAINTS` | Primary, unique, foreign-key, and check constraints. |
| `KEY_COLUMN_USAGE` | Constraint key positions and referenced columns. |
| `REFERENTIAL_CONSTRAINTS` | Foreign-key update, delete, and enforcement details. |
| `CHECK_CONSTRAINTS` | Check-constraint names and canonical expressions. |
| `CHARACTER_SETS` | The one supported character set, `utf8mb4`. |
| `COLLATIONS` | `utf8mb4_0900_ai_ci` and `utf8mb4_bin`. |

The already-set `PROCESSLIST`, `ACCOUNTS`, and `ACCOUNT_GRANTS` views remain
supported project extensions under their existing visibility rules. They do
not change the standard catalog vocabulary. `SHOW TABLES FROM
information_schema` exposes the complete supported view set, including these
extensions, and row-level authorization still applies inside the administrative
views.

No empty compatibility views are provided for unsupported features, including
views, routines, triggers, events, partitions, plugins, files, or InnoDB
internals. A reference to one of those absent views fails as a missing table;
it does not return invented or permanently empty metadata.

`CHARACTER_SETS` exposes only `utf8mb4`. `COLLATIONS` exposes only
`utf8mb4_0900_ai_ci` and `utf8mb4_bin`, with their already-set default and
comparison meanings.

Every supported view is read-only and queryable through ordinary `SELECT`.
Predicates, joins, grouping, ordering, and prepared parameters are supported
by the catalog surface. A catalog view cannot be the target of a mutation.

## Supported catalog `SHOW` statements

The closed catalog `SHOW` surface is:

```text
SHOW DATABASES
SHOW CREATE DATABASE
SHOW [FULL] TABLES
SHOW [FULL] COLUMNS
DESCRIBE
SHOW INDEX
SHOW CREATE TABLE
SHOW CHARACTER SET
SHOW COLLATION
```

The supported forms use the MySQL 8.4.11 `FROM`/`IN`, `LIKE`, and `WHERE`
clauses wherever those forms apply. `SHOW DATABASES` and `SCHEMATA` have the
same visibility boundary. `SHOW [FULL] PROCESSLIST`, `SHOW WARNINGS`,
`SHOW SESSION VARIABLES`, `SHOW GLOBAL VARIABLES`, and `SHOW GRANTS` remain
governed by their owning contracts; this document does not redefine them.

Other `SHOW` families, including `SHOW TABLE STATUS` and `SHOW ENGINES`, are
unsupported and fail explicitly. The absence of `SHOW ENGINES` is deliberate:
storage-engine choice is not an application-facing v0.1 feature.

## MySQL shape, naming, and ordering

Standard `SHOW` results and standard `information_schema` views use the exact
MySQL 8.4.11 public column names, order, logical types, nullability, and
meanings wherever the corresponding v0.1 concept exists. Unsupported facts are
represented by absent rows or documented `NULL` fields, never guessed or
fabricated values.

Catalog identifiers preserve their declared spelling. Lookup follows the
portable [SQL identifier](../CONTEXT.md) rule. Catalog-name fields report
`def`, matching the compatibility baseline. Standard view and column names use
their MySQL spellings; the project-specific `ACCOUNTS` and `ACCOUNT_GRANTS`
views retain their explicitly defined schemas.

Supported `SHOW` statements use MySQL-compatible deterministic ordering.
Ordinary `information_schema` queries have no ordering guarantee unless the
query supplies `ORDER BY`.

## Table and column metadata

`information_schema.TABLES` reports:

- `TABLE_TYPE` as `BASE TABLE` for persistent relational tables and `SYSTEM
  VIEW` for supported `information_schema` views.
- `ENGINE` as the stable, honest product identifier `DATABASE`; it never claims
  to be `InnoDB`.
- `TABLE_ROWS` as an estimate derived from server-managed planning statistics,
  not an exact count.
- The next `AUTO_INCREMENT` value, table collation, and table comment when
  applicable.
- `NULL` for physical layout and maintenance fields without public v0.1
  semantics, including row format, average row length, data and index byte
  allocation, free space, checksums, and create/update/check timestamps.
- An empty `CREATE_OPTIONS` value.

`information_schema.COLUMNS` exposes ordinal position, the declared and
complete type, default, nullability, character set, collation, numeric and
temporal precision, comment, key classification, `AUTO_INCREMENT`, and
temporal `ON UPDATE` behaviour. Generated-column fields are empty because
generated columns are outside v0.1.

`COLUMNS.PRIVILEGES` projects the current account's namespace grants into
MySQL-shaped column capabilities:

| Grant | `COLUMNS.PRIVILEGES` contribution |
| --- | --- |
| `DATA_READ` | `select` |
| `DATA_WRITE` | `insert,update` |
| `SCHEMA_MANAGEMENT` | `references` |

Delete and schema-alter authority have no column-level spelling and are not
invented in this field.

## Indexes and constraints

`information_schema.STATISTICS` exposes uniqueness, index and key-part order,
column or functional expression, prefix length, ascending or descending
direction, visibility, B-tree type, nullability, comments, and estimated
cardinality. Cardinality is an estimate from server-managed planning
statistics. Query explanations, rather than this view, remain the surface for
provenance, freshness, and limitations of that estimate.

The constraint views expose primary, unique, foreign-key, and check
relationships, including referenced columns, key positions, update and delete
actions, enforcement, and canonical check expressions.

Server-assigned names are stable, persisted parts of the public schema. They
appear consistently in catalog results, `SHOW CREATE TABLE`, errors, and later
schema-alteration statements:

- An unnamed primary key is named `PRIMARY`.
- Other unnamed indexes use MySQL-shaped column-derived names with deterministic
  suffixes for collisions.
- Unnamed foreign keys use `<table>_ibfk_<n>`.
- Unnamed checks use `<table>_chk_<n>`.

Generation and collision handling follow the MySQL 8.4.11 baseline subject to
the project's portable identifier rules and limits.

## Canonical schema definitions

`SHOW CREATE DATABASE` and `SHOW CREATE TABLE` return a [canonical schema
definition](../CONTEXT.md): stable, replayable SQL representing the current
public schema rather than the originally submitted text.

The output preserves declared identifier spelling, comments, defaults,
collations, constraints, indexes, functional expressions, temporal update
clauses, and the current `AUTO_INCREMENT` value. It omits implementation
details and unsupported physical clauses, including `ENGINE`, so the returned
statement is accepted by this product and recreates the same public schema
meaning.

## Consistency, authorization, and failures

Every catalog statement observes one internally consistent snapshot of
committed schema and authorization at statement start. Relationships are never
torn across rows. Catalog visibility is statement-scoped, not frozen for an
entire transaction: a later statement may observe committed DDL or grant
changes under the settled statement-boundary authorization rule.

When an account directly names a namespace, an existing but unauthorized
namespace returns the MySQL access-denied outcome. A nonexistent namespace
returns the MySQL unknown-database outcome. Enumeration never reveals either
the unauthorized namespace or its contents.

Equivalent supported operations use the MySQL 8.4.11 numeric error identity and
SQLSTATE. Message wording is not stable. Unsupported statements, absent views,
reserved namespaces, read-only catalog mutations, and unsupported physical
features fail explicitly rather than being silently accepted.

This contract intentionally does not prescribe how namespace state, catalog
rows, planning estimates, canonical SQL, or authorization filtering are
represented internally.
