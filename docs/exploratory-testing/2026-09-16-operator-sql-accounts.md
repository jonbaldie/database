# Exploratory Testing: Operator, SQL, and Accounts

Date: 2026-09-16

Status: complete

Confirmed product bugs: none

## Scope

This pass used the supported operator command family, MySQL wire protocol, SQL,
and diagnostics HTTP listener. It covered three critical user journeys:

- An operator initializes, starts, checks, and stops one database instance.
- An application developer creates data, reads and changes rows, explains a
  query, and confirms data after restart.
- An account manager grants and removes access, changes a password, and checks
  namespace visibility.

Transactions and savepoints, online backup and restore, upgrade, configuration
precedence, data corruption, and the full external client profile were not
explored in this pass.

## Conditions

- Repository commit: `5c43fc5fd0afd3c5601e22670664426100d0957c`.
- Product version: `0.2.12`.
- Build identity: `local`.
- Platform: `darwin/arm64`.
- Go version: `go1.26.6`.
- Run identifier: `1789514612-81819`.
- MySQL listener: `127.0.0.1:59257`.
- Diagnostics listener: `127.0.0.1:59258`.
- Evidence directory: `/tmp/verify-database-evidence/1789514612-81819`.
- The data directory and credentials were generated for this run only.
- `control.sh doctor` passed the process, version, ownership, readiness,
  liveness, lock, and authentication checks before the journeys.

The isolated data directory was removed after the pass. The evidence directory
was kept. It was 188K at cleanup time. No generated artifact over 100 MB
remained.

## Journey 1: Operator And Diagnostics

### Goal

The operator must be able to start one instance, identify its readiness, reject
unsafe or conflicting operations, validate stored data, and stop the instance
without leaving the diagnostics listener available.

### Actions and results

1. `database version --format=json` returned a `database.version/v1` record for
   product version `0.2.12` and data compatibility `0.1.x`.
2. The verification launcher initialized a new data directory and started
   `serve` with JSON lifecycle output. A `server.ready` event was present.
3. `/live` returned `{"status":"live"}` and `/ready` returned
   `{"status":"ready"}`.
4. `/metrics` contained the process, server, execution, temporary-storage, and
   resource counter series required by the diagnostics contract.
5. An inline password passed to `init` was rejected with exit class
   `invalid_input` and exit code `2`. No rejected data directory was created.
6. A second `serve` using the live data directory was rejected. The product
   returned exit class `precondition` and exit code `3`. The first server
   remained available.
7. `database data validate` returned `success`, `valid: true`, data version
   `0.1.0`, and no findings.
8. After SQL work, the metrics response showed a non-zero
   `database_execution_memory_peak_bytes` value.
9. A graceful stop produced `server.stopping`, `server.stopped`, and a
   successful `database.operator.result/v1` record. A request to `/ready` after
   the stop failed to connect.

### Evidence

- [version](file:///tmp/verify-database-evidence/1789514612-81819/operator-lifecycle/version.json)
- [ready lifecycle event](file:///tmp/verify-database-evidence/1789514612-81819/operator-lifecycle/lifecycle-ready.jsonl)
- [inline password rejection](file:///tmp/verify-database-evidence/1789514612-81819/operator-lifecycle/inline-password-rejected.txt)
- [second owner rejection](file:///tmp/verify-database-evidence/1789514612-81819/operator-lifecycle/second-serve-rejected.txt)
- [data validation](file:///tmp/verify-database-evidence/1789514612-81819/operator-lifecycle/data-validate.json)
- [metrics before SQL](file:///tmp/verify-database-evidence/1789514612-81819/diagnostics-endpoints/metrics-before.txt)
- [metrics after SQL](file:///tmp/verify-database-evidence/1789514612-81819/diagnostics-endpoints/metrics-after-query.txt)
- [graceful stop](file:///tmp/verify-database-evidence/1789514612-81819/operator-lifecycle/graceful-stop.txt)
- [final lifecycle records](file:///tmp/verify-database-evidence/1789514612-81819/operator-lifecycle/lifecycle-final.jsonl)
- [diagnostics after stop](file:///tmp/verify-database-evidence/1789514612-81819/diagnostics-endpoints/ready-after-stop.txt)

### Candidate classification

The maintained operator recipe says that the second `serve` failure should use
exit class `operation_failed` and exit code `6`. The normative operator
contract classifies a state or exclusivity precondition as `precondition` and
exit code `3`. The observed product result matches the normative contract.
This is a verification-document mismatch, not a confirmed product bug.

## Journey 2: SQL, Query Explanation, And Durability

### Goal

An application developer must be able to create a database and table, write and
read rows, correct invalid input, inspect query work, and find committed data
after a graceful restart.

### Actions and results

1. Created `shop` and `orders (id INT PRIMARY KEY, total INT NOT NULL)`.
2. Inserted rows `(1, 250)` and `(2, 100)`, then read both rows in key order.
3. The duplicate key insert failed with error `1062`. The null `NOT NULL` value
   failed with error `1048`. A missing table failed with error `1146`.
4. The session remained usable after the failures. It inserted row `3`, changed
   row `1` to total `300`, deleted row `2`, and read the final rows
   `(1, 300)` and `(3, 99)`.
5. Traditional `EXPLAIN` returned the MySQL columns and product operator
   columns. `EXPLAIN FORMAT=JSON` returned `format_version: 1`, mode `plan`, a
   project/filter/scan tree, and the selected `PRIMARY` B-tree path.
6. `EXPLAIN ANALYZE` returned actual row and timing columns. The JSON schema
   validation command passed for all committed examples.
7. `EXPLAIN FORMAT=JSON` for an `UPDATE` reported `read_only: false` but did
   not change the row. A follow-up read still returned total `300`.
8. The on-disk catalog showed the `shop` namespace, `orders` definition, and
   constraints. After restart, a fresh MySQL session read `(1, 300)` and
   `(3, 99)`.

### Evidence

- [schema creation](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/schema-create.jsonl)
- [insert and read](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/insert-read.jsonl)
- [negative cases and recovery](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/negative-corrections.jsonl)
- [update and delete](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/update-delete.jsonl)
- [traditional explanation](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/explain-traditional.jsonl)
- [JSON explanation](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/explain-json.jsonl)
- [parsed JSON explanation](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/explain-json-parsed-correct.json)
- [analyzed explanation](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/explain-analyze.jsonl)
- [write explanation with no mutation](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/explain-write-no-mutation.jsonl)
- [explanation schema validation](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/query-explanation-contract.txt)
- [catalog before restart](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/catalog-before-restart.json)
- [restart](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/restart.txt)
- [read after restart](file:///tmp/verify-database-evidence/1789514612-81819/sql-data-query/read-after-restart.jsonl)

### Candidate classification

The SQL feature recipe says that `control.sh catalog` contains stored row data.
The observed catalog contains schema and account metadata, but row data is
stored outside that metadata. The catalog metadata contract says that namespace
visibility never exposes row data. The independent post-restart read proved the
mutation and durability. This is a verification-document mismatch, not a
confirmed product bug.

The first local `jq` parsing command failed because it read the preceding
`USE shop` result, which has no `rows` field. The product explanation was valid.
Selecting the explanation line and parsing it again succeeded. This was a
driver observation error and is rejected as a product failure.

## Journey 3: Account Permissions And Visibility

### Goal

An account manager must be able to create a read-only account, enforce its
permission boundary, revoke access, rotate its password, protect the last
account manager, and retain the account state after restart.

### Actions and results

1. Created account `reader` and granted `DATA_READ` on `shop.*`.
2. The reader selected the stored rows. `SHOW GRANTS` showed only the read
   grant. `SHOW DATABASES` showed `information_schema` and `shop`.
3. A reader `INSERT` failed with error `1044`. The table remained unchanged.
4. After `REVOKE DATA_READ`, a fresh reader session failed to select with error
   `1044`.
5. Re-granted the read privilege and changed the password. The old password
   failed with error `1045`. The new password authenticated and read the rows.
6. Revoking `ACCOUNT_MANAGER` from the only manager failed with error `1396`.
7. Created an ungranted `secret` namespace. The reader did not see it in
   `SHOW DATABASES`, `information_schema.SCHEMATA`, or
   `information_schema.TABLES`. `SHOW CREATE DATABASE secret` and a qualified
   read failed with error `1044`.
8. After restart, the changed password, read grant, and stored rows remained
   available to the reader.

### Evidence

- [account creation and grant](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/create-grant.jsonl)
- [granted read and metadata](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/reader-granted-read-metadata.jsonl)
- [write denial](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/reader-write-denied.jsonl)
- [revoke](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/revoke-read.jsonl)
- [read after revoke](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/reader-after-revoke.jsonl)
- [password change](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/regrant-alter-password.jsonl)
- [old password rejection](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/old-password-rejected.txt)
- [new password read](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/new-password-read.jsonl)
- [last manager protection](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/last-manager-revoke.jsonl)
- [hidden namespace metadata](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/reader-hidden-namespace-metadata.jsonl)
- [hidden namespace qualified access](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/reader-hidden-namespace-qualified-access.jsonl)
- [reader after restart](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/reader-after-restart.jsonl)
- [account catalog after restart](file:///tmp/verify-database-evidence/1789514612-81819/account-administration/catalog-after-restart.json)

### Candidate classification

The reader could issue `USE secret`, but it could not read data from that
namespace. The catalog contract requires denial for direct catalog access, and
the direct catalog and qualified data checks returned `1044`. No failure was
confirmed.

## Usability Observations

- The verification launcher gave one run identifier, free ports, a disposable
  password file, and an evidence directory. This made isolation clear.
- Stable JSON records and MySQL error codes made positive and negative results
  easy to check.
- The `control.sh sql` helper prints one JSON record for each statement. A
  follow-up read is needed because `rows_affected` is not populated by the
  helper.
- The maintained feature recipes need alignment with the normative operator
  exit-class contract and the current row-storage evidence rule.

The last item is a suggested documentation improvement. It is not a product
failure observed in this pass.

## Issue Tracker

No product bug was confirmed. The two verification-document mismatches were
filed as [GitHub issue #393](https://github.com/jonbaldie/database/issues/393).

## Limitations

- The pass used the Go `sqlclient` helper and did not exercise PHP, Node.js,
  Python, Java, or the MySQL command-line client.
- It did not test concurrent transactions, lock waits, savepoints, backup and
  restore, upgrade, or configuration changes.
- Evidence is stored in the local temporary evidence directory. The directory
  is not part of the repository.
