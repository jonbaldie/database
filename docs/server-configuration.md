# Server configuration

This document defines the closed v0.1 **server configuration registry**. It
contains only durable-state location, public endpoints and TLS, log
presentation, and operator resource ceilings. SQL semantics, durability,
authentication method, recovery, planning, storage behaviour, collations,
internal caches, scheduling, and other implementation tuning are fixed product
behaviour, not configuration modes or knobs.

`database serve` and `database config validate` consume this registry. Offline
commands receive data-directory and artifact locations as explicit command
inputs; ambient server configuration does not redirect them.

## Sources and precedence

The configuration file is optional TOML. It is loaded only when selected by
`--config PATH` or `DATABASE_SERVER_CONFIG`; the flag wins. v0.1 does not
search an ambient filename or system directory. The selector is not a registry
setting. The accepted v0.1 TOML subset is flat: one canonical top-level key per
assignment, quoted strings for path, address, and enum settings, and bare
positive decimal integers for numeric settings. Tables, arrays, and other TOML
value types are not registry forms.

Each registry setting has exactly three forms:

| Source | Form |
| --- | --- |
| TOML | Canonical `lower_snake_case` name below |
| Environment | `DATABASE_SERVER_` plus canonical name in uppercase |
| Flag | Canonical name with underscores changed to hyphens, prefixed by `--` |

For example, `max_connections`, `DATABASE_SERVER_MAX_CONNECTIONS`, and
`--max-connections` select the same setting. Precedence is flag, environment,
TOML file, then default. A missing higher-precedence value does not erase a
lower-precedence value.

Unknown TOML keys, `DATABASE_SERVER_` variables, and flags, as well as
duplicate TOML keys, repeated flags, empty or malformed values, and
contradictory settings fail validation and startup. Unrelated environment
variables are ignored.

## Registry

| Canonical name | Type | Default | Allowed value |
| --- | --- | ---: | --- |
| `data_directory` | absolute path | required | One non-empty absolute path |
| `mysql_listen_address` | network address | `127.0.0.1:3306` | One IPv4 address and port or bracketed IPv6 address and port; port 1–65535 |
| `tls_certificate_file` | absolute path | unset | One readable PEM certificate-and-optional-chain file; valid only with `tls_private_key_file` |
| `tls_private_key_file` | absolute path | unset | One readable unencrypted PEM private-key file matching the certificate; valid only with `tls_certificate_file` |
| `diagnostics_listen_address` | network address | unset | One IPv4 address and port or bracketed IPv6 address and port; port 1–65535 |
| `log_format` | enum | `json` | `json` or `text` |
| `statement_timeout_ms` | integer milliseconds | `300000` | 1–9,223,372,036,854,775,807 |
| `lock_wait_timeout_ms` | integer milliseconds | `5000` | 1–9,223,372,036,854,775,807 |
| `idle_in_transaction_timeout_ms` | integer milliseconds | `300000` | 1–9,223,372,036,854,775,807 |
| `idle_session_timeout_ms` | integer milliseconds | `3600000` | 1–9,223,372,036,854,775,807 |
| `execution_memory_limit_bytes` | integer bytes | `67108864` | 1–9,223,372,036,854,775,807 |
| `aggregate_execution_memory_limit_bytes` | integer bytes | `2147483648` | 1–9,223,372,036,854,775,807 |
| `temporary_storage_limit_bytes` | integer bytes | `17179869184` | 1–9,223,372,036,854,775,807 |
| `aggregate_temporary_storage_limit_bytes` | integer bytes | `34359738368` | 1–9,223,372,036,854,775,807 |
| `max_connections` | integer count | `100` | 1–2,147,483,647 ordinary authenticated sessions |
| `max_allowed_packet` | integer bytes | `67108864` | 1,024–1,073,741,824 |
| `max_prepared_stmt_count` | integer count | `4096` | 1–2,147,483,647 server-prepared statements |

All numeric forms are positive base-10 integers in the named unit. Zero never
means unlimited or disables a safeguard. `execution_memory_limit_bytes` cannot
exceed `aggregate_execution_memory_limit_bytes`; `temporary_storage_limit_bytes`
cannot exceed `aggregate_temporary_storage_limit_bytes`. The emergency
operational session is additional to `max_connections` and has no separate
setting.

The v0.1 contract is that configured statement, lock-wait, execution-memory,
and temporary-storage values become the running server defaults and ceilings
exposed by the **session settings registry**, which is distinct from this
server configuration registry. Sessions may tighten them but cannot exceed or
disable them. Idle timeouts, connection count, packet size, aggregate budgets,
and prepared-statement count are read-only to sessions. The server applies
`lock_wait_timeout_ms` to conflicting row-lock waits. The remaining runtime
and session enforcement is defined by the SQL and session-settings contracts.

## Network, TLS, logging, and secrets

v0.1 has one MySQL TCP listener. Hostnames, Unix sockets, multiple application
listeners, and automatic port assignment are unsupported. Native and OCI
defaults are the same; a container deployment explicitly selects
`0.0.0.0:3306` or `[::]:3306` when needed.

TLS is disabled when both TLS paths are unset and enabled when both are valid.
One path, an unreadable or invalid file, or a mismatched pair fails validation
or startup. TLS 1.2 and TLS 1.3 are supported. Cipher selection,
client-certificate authentication, inline certificate or key material, and live
reload are unsupported. A restart applies changed TLS files. The v0.1 contract
requires a prominent structured warning for a non-loopback MySQL listener
without TLS. The `ready` record in `database.lifecycle/v1` carries one warning
with code `UNSAFE_NON_TLS_LISTENER`, severity `warning`, a stable summary, and
the non-sensitive context fields `address` and `tls=disabled`. Human output
prints the same warning prominently. Loopback listeners and TLS-enabled
listeners do not emit it. Configuration validation remains read-only; the
warning is emitted only when the service starts.

Leaving `diagnostics_listen_address` unset disables diagnostics. Setting it
enables the documented non-sensitive liveness, readiness, and Prometheus metric
surface; diagnostics has no TLS, authentication, path, or metric-selection
settings. `GET` and `HEAD` are supported for `/live`, `/ready`, and `/metrics`;
other methods return `405`, and unknown paths return `404`. `/live` remains
successful while the process can answer. `/ready` returns `503` with a coarse
`reason` while the process is `starting`, `recovering`, `shutting_down`, or
`corruption`. The diagnostics and MySQL settings cannot select the same
listener.
Alongside readiness, the metrics surface reports current and peak execution
memory and temporary-storage reservations, plus cumulative spill, cancellation,
timeout, execution-memory-exhaustion, and temporary-storage-exhaustion counts.
It contains no SQL text, bound values, session identifiers, or temporary-file
paths.

Lifecycle JSON records retain the `database.lifecycle/v1` schema and include
stable `event_code` and `severity` fields for startup, recovery, readiness,
shutdown, corruption, and exceptional outcomes. Their messages are for human
context and are not stable identifiers; records never contain secrets or SQL.

The v0.1 logging contract sends logs to standard error. `json` is the default
structured presentation; `text` is the equivalent human presentation. There is
no log destination, rotation, retention, or verbosity setting. Neither
presentation emits passwords, credential hashes, TLS or RSA key material,
bound parameter values, or raw SQL text. Configuration contains no inline
password, credential hash, or private-key material. The resolver validates
`log_format`; the running logging sink and event schema are #31 work. Command
output format is independent of `log_format`. `database config validate`
reports the effective settings and sources while redacting the private-key path
and any future value classified as secret.

## Validation and evolution

`database config validate` uses the same names, values, precedence, and
cross-setting rules as startup without changing durable state. Startup repeats
environmental checks such as data-directory state, endpoint availability, file
readability, and certificate/key validity.

The target binary defines the closed accepted schema; there is no separate
configuration-version field. Unknown settings fail. `0.x` registry changes
follow the documented breaking-change policy, and operators validate with the
target binary before upgrading.

TOML parsing, flag parsing, socket setup, certificate loading, timer
representation, budget enforcement, and internal tuning are implementation
choices, provided they preserve this observable contract.
