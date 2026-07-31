# v0.1 session settings contract

This document defines the published MySQL session settings registry. Names are
case-insensitive. Results use lower-case names with underscores. Unknown names,
old aliases, wrong values, and changes that weaken a server limit fail.

## Mutable session settings

| Name | Default | Supported values |
| --- | --- | --- |
| `autocommit` | `ON` | `ON`, `OFF` |
| `transaction_isolation` | `REPEATABLE-READ` | `REPEATABLE-READ`, `READ-COMMITTED` |
| `transaction_read_only` | `OFF` | `ON`, `OFF` |
| `time_zone` | `+00:00` | `UTC`, or an offset from `-13:59` to `+14:00` |
| `collation_connection` | `utf8mb4_0900_ai_ci` | `utf8mb4_0900_ai_ci`, `utf8mb4_bin` |
| `statement_timeout_ms` | `300000` | positive value at or below the server ceiling |
| `lock_wait_timeout_ms` | `5000` | positive value at or below the server ceiling |
| `execution_memory_limit_bytes` | `67108864` | positive value at or below the server ceiling |
| `temporary_storage_limit_bytes` | `17179869184` | positive value at or below the server ceiling |

`SET NAMES utf8mb4 [COLLATE name]` sets the three client character settings
and the connection collation. `SET name = DEFAULT` restores the server value.
Session setting changes are not part of a transaction. A rollback does not undo
them. `COM_RESET_CONNECTION` rolls back work, removes prepared statements, and
restores all session values and the initial namespace.

## Discovery and fixed values

`SELECT @@name`, `@@session.name`, and `@@global.name` read only this registry.
`SHOW SESSION VARIABLES` and `SHOW GLOBAL VARIABLES` show the same registry.
Global values are server defaults. `SET GLOBAL` is unsupported.

`sql_mode` has the fixed value
`STRICT_ALL_TABLES,ONLY_FULL_GROUP_BY,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO`.
The client character values are fixed to `utf8mb4`. Server limits, server
character values, product identity, and protocol identity are read-only.

Transaction settings can change only outside an active transaction. A read-only
transaction rejects writes, schema changes, and locking reads before it changes
data. A session can make a timeout or capacity lower, but it cannot make it
higher than the server value.
