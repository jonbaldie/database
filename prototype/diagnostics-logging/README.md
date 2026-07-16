# PROTOTYPE — v0.1 diagnostics and logging contract

> Throwaway product-contract prototype. This records the product decisions accepted while resolving **Define the v0.1 diagnostics and logging contract**; it is a primary source for the final specification, not production documentation or implementation code.

## Boundary

The diagnostics surface is opt-in and listens on the separately configured diagnostics address. It has no authentication or TLS in v0.1, so it exposes only coarse, non-sensitive operational state. Logs go to standard error. JSON is the default log format; the optional text format represents the same product events for humans.

The surface does not expose SQL text, account or application identifiers, application data, storage-engine details, or runtime-specific implementation metrics. Authenticated SQL operational introspection remains the richer interface for authorized operators.

## Health and metrics paths

The diagnostics listener supports these exact paths:

| Request | Successful outcome |
| --- | --- |
| `GET /live` | `200 application/json` with `{"status":"live"}` whenever the diagnostics surface can respond |
| `GET /ready` | `200 application/json` with `{"status":"ready"}` only while the server accepts normal authenticated database work |
| `GET /metrics` | `200` with a valid Prometheus text exposition whenever a metric snapshot can be produced |

`HEAD` returns the corresponding status and headers without a body. Other paths return `404`; methods other than `GET` and `HEAD` return `405`.

While the server is not ready, `/ready` returns `503 application/json` with:

```json
{"status":"not_ready","reason":"<reason>"}
```

The closed v0.1 reasons are `starting`, `recovering`, `upgrading`, `shutting_down`, and `corruption`. The status code is authoritative; the reason supplies coarse operational context.

Liveness means only that the diagnostics surface can answer. `/live` remains successful during startup, recovery, upgrade, shutdown, and corruption until the diagnostics listener closes. `/metrics` likewise remains available while the server is unready. If a valid metric exposition cannot be produced, `/metrics` returns `500` rather than partial or invalid output. A failed scrape does not alter readiness or application behaviour.

All diagnostics responses are read-only. Metrics describe the current server process; counters reset when it restarts, and v0.1 provides no durable metric history.

## Closed Prometheus metric set

All names, types, meanings, label names, and units below are public product contracts.

| Metric | Type | Meaning |
| --- | --- | --- |
| `database_server_info{version}` | gauge | Constant `1` identifying the database release |
| `database_server_start_time_seconds` | gauge | Process start time as Unix epoch seconds |
| `database_server_ready` | gauge | `1` when `/ready` would succeed, otherwise `0` |
| `database_server_sessions{state}` | gauge | Current ordinary authenticated sessions by state |
| `database_server_connections_total{outcome}` | counter | Connection attempts by terminal admission outcome |
| `database_server_statements_active` | gauge | Statements currently executing |
| `database_server_statements_total{outcome}` | counter | Completed statements by terminal outcome |
| `database_server_statement_duration_seconds{outcome}` | histogram | Elapsed duration of completed statements, including lock waits and temporary-resource work |
| `database_server_transactions_active` | gauge | Current open transactions |
| `database_server_transactions_total{outcome}` | counter | Completed transactions by commit or rollback outcome |
| `database_server_deadlocks_total` | counter | Detected deadlocks |
| `database_server_execution_memory_bytes{kind}` | gauge | Current aggregate execution-memory use or configured server limit |
| `database_server_temporary_storage_bytes{kind}` | gauge | Current aggregate temporary-storage use or configured server limit |
| `database_server_spills_total` | counter | Completed statements that used query spill, counted once per statement |
| `database_server_resource_exhaustions_total{resource}` | counter | Operations rejected or failed because a public resource ceiling was reached |
| `database_server_recovery_in_progress` | gauge | `1` while automatic crash recovery is running, otherwise `0` |
| `database_server_recoveries_total{outcome}` | counter | Automatic recovery attempts by terminal outcome |
| `database_server_last_recovery_duration_seconds` | gauge | Duration of the most recently completed recovery; absent before one completes |
| `database_server_corruption_detected` | gauge | `1` after this process detects blocking durable corruption, otherwise `0` |
| `database_server_backup_in_progress` | gauge | `1` while an online full-server backup is running, otherwise `0` |
| `database_server_backups_total{outcome}` | counter | Online full-server backup attempts by terminal outcome |

### Closed label values

- `state`: `active`, `idle`, `idle_in_transaction`
- Connection `outcome`: `accepted`, `capacity_rejected`, `authentication_failed`
- Statement `outcome`: `succeeded`, `failed`, `timed_out`, `cancelled`, `resource_exhausted`
- Transaction `outcome`: `committed`, `rolled_back`
- Recovery and backup `outcome`: `succeeded`, `failed`, `cancelled`
- `resource`: `execution_memory`, `temporary_storage`, `connections`, `prepared_statements`, `packet`
- `kind`: `used`, `limit`

The statement-duration histogram has buckets at 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, and 300 seconds, plus `+Inf`.

Metrics never use account, session, operation, namespace, object, query, network-address, filesystem-path, or other unbounded identifiers as labels. Go runtime, storage-engine, cache, scheduler, and other implementation-specific metrics are outside the public v0.1 surface.

## Server log records

The server log contract belongs to `database serve`. Finite operator commands retain their separate, already-defined result, progress, and diagnostic schemas rather than duplicating those records as server logs.

Every JSON log record contains:

| Field | Contract |
| --- | --- |
| `record_type` | Exact value `database.server.log/v1` |
| `timestamp` | UTC RFC 3339 timestamp |
| `severity` | One of `info`, `warning`, `error`, or `critical` |
| `event_code` | Stable event identity from the catalog below |
| `message` | Human-readable summary whose wording is not stable |

Absent optional fields are omitted rather than represented as `null`. Event-specific records may carry `instance_id`, `operation_id`, `session_id`, `query_fingerprint`, `fingerprint_version`, `remote_address`, `account_name`, and documented event context.

Severity has these meanings:

- `info`: an expected operational transition or requested action;
- `warning`: service continues, but an outcome or configuration merits operator attention;
- `error`: an operation or supported surface failed while some service may remain available;
- `critical`: normal service cannot safely continue.

The text format represents the same events and includes their event codes, but its layout and wording are not compatibility guarantees.

## Required event-code catalog

| Event code | Usual severity | Product meaning |
| --- | --- | --- |
| `server.starting` | `info` | Server startup began |
| `server.ready` | `info` | Server began accepting normal authenticated work |
| `server.unready` | `warning` | A ready server became unready; carries a readiness reason |
| `server.stopping` | `info` | Graceful shutdown began |
| `server.stopped` | `info` | Graceful shutdown reached its stopped state |
| `server.start_failed` | `critical` | Startup failed before readiness |
| `network.mysql_without_tls` | `warning` | The MySQL listener is non-loopback while TLS is disabled |
| `diagnostics.failed` | `error` | The enabled diagnostics surface failed after startup |
| `recovery.started` | `info` | Automatic crash recovery began |
| `recovery.completed` | `info` | Automatic crash recovery completed successfully |
| `recovery.failed` | `critical` | Recovery failed and normal service is unavailable |
| `corruption.detected` | `critical` | Durable corruption was detected and normal service is blocked |
| `authentication.failed` | `warning` | A connection attempt failed authentication without revealing why |
| `session.rejected` | `warning` | A session was rejected for an operational reason such as capacity or shutdown |
| `session.terminated` | `info` | The server terminated a session; carries a stable reason |
| `statement.timed_out` | `warning` | A statement reached its execution deadline |
| `statement.cancelled` | `info` | A statement was cancelled by an application or operator |
| `statement.resource_exhausted` | `warning` | A statement failed because a public execution resource was exhausted |
| `deadlock.detected` | `warning` | A deadlock was detected and a victim selected under the transaction contract |
| `backup.started` | `info` | An online full-server backup began |
| `backup.completed` | `info` | An online full-server backup completed successfully |
| `backup.failed` | `error` | An online full-server backup failed |
| `shutdown.requested` | `info` | An authenticated operator requested graceful shutdown |
| `log.events_suppressed` | `warning` | Repetitive event records were suppressed; carries the affected code, count, and interval |

Lifecycle, recovery, corruption, backup, and shutdown events are never suppressed. Repetitive authentication, admission, timeout, cancellation, deadlock, and resource-exhaustion events may be rate-limited only when `log.events_suppressed` reports the affected `event_code`, suppressed count, and interval.

Server lifecycle and recovery events carry `instance_id` once it is known. Operator-triggered events carry `operation_id`; session and statement events carry `session_id`; statement events carry `query_fingerprint` and `fingerprint_version`. Authentication and admission events carry `remote_address` and the submitted `account_name`, but never state whether that account exists.

Logs are operational diagnostics, not an audit trail or query log. They do not record successful statements, transaction-by-transaction activity, row changes, routine session traffic, or account and schema administration as durable history.

## Sensitivity contract

No health response, metric, or log record contains passwords, credential hashes, operator secret input, private keys, raw SQL, normalized SQL, bound parameters, row or result values, protocol payloads, environment contents, or raw configuration-file contents.

Health and metric surfaces contain no account names, remote addresses, database namespaces, object names, session identities, operation identities, or query fingerprints. Server logs may include account names, remote addresses, namespace names, and object names only where an authentication, authorization, or corruption event would otherwise be materially less actionable. Logs are therefore sensitive operational artifacts that operators must protect through their external collection, access, and retention controls.

A query fingerprint is an opaque statement-structure identity. Equal statement structures produce the same fingerprint regardless of literal or bound values; prepared and text execution agree; and fingerprints remain comparable across restarts and server instances within `0.1.x`. `fingerprint_version` identifies its comparison semantics. The normalized representation used to obtain a fingerprint is never emitted.

## Delivery and compatibility

Records from one server process preserve occurrence order. Logs are not durable, transactional, exactly-once, or permanent history, and abrupt process loss may lose final records. Log-delivery failure does not weaken commit durability or change query outcomes.

Within `0.1.x`:

- health paths, HTTP meanings, existing JSON fields, and existing readiness-reason meanings remain compatible; HTTP status is authoritative, and new reason values may be added;
- metric names, types, meanings, units, and label names remain stable; new metrics and documented label values may be added, but labels cannot be added to an existing metric;
- JSON log required fields, event-code meanings, severity meanings, and documented event fields remain stable; new event codes and optional fields may be added;
- human message wording and text-log presentation may change without compatibility significance; and
- an incompatible JSON log shape receives a new `record_type`.

These diagnostics and logging contracts are project interfaces, not claims of MySQL compatibility. Their implementation, collection, serialization, and internal instrumentation remain unspecified.
