# v0.1 operator automation contract

This document is the normative v0.1 contract for automating the supported
operator workflows. It fixes the command inputs, secret boundary, streams,
progress records, terminal results, diagnostics, exit classes, and compatibility
rules that an automation client may rely on. It does not prescribe command
parsers, process structure, control channels, credential-reading libraries,
workflow algorithms, or serialization implementation beyond the observable
records described here.

The single supported [`database` operator command family](../CONTEXT.md) uses
one command and one result contract for initialization, service lifecycle,
backup, restore, upgrade, configuration, validation, inspection, and version
reporting. Every invocation has an [operator operation identity](../CONTEXT.md)
and reaches either a documented terminal result or an explicit process-loss
boundary.

## Common command and stream contract

Every command accepts these common presentation options:

| Option | Values | Default | Meaning |
| --- | --- | --- | --- |
| `--result` | `human`, `json` | `human` | Selects the one terminal result on standard output. |
| `--progress` | `auto`, `human`, `json`, `none` | `auto` | Selects optional progress on standard error. |

The machine form is written with an equals value, for example
`--result=json --progress=json`; a separated option value has the same meaning
when accepted by the command parser.

`auto` emits human progress when standard error is a terminal and emits no
progress when it is not a terminal. Non-interactive automation receives
machine-readable progress only when it explicitly requests
`--progress=json`.

Standard output contains one terminal result. With `--result=json`, it is
exactly one `database.operator.result/v1` object. Standard error carries
optional progress and diagnostics; JSON progress is newline-delimited and never
contaminates the result stream. Human wording is for people and is not an
automation contract.

If `--result=json` is recognizable, invalid invocation and validation failures
also use the result envelope. An invalid result-format request, or a failure
before command processing can identify the requested format, may use ordinary
error output.

A successful result means that the workflow reached its documented terminal
state. Request acceptance and progress are not success. There is no `complete`
progress phase: for `serve`, `ready` is progress and the successful terminal
result is emitted only after graceful stop. An abrupt process loss may leave
progress without a terminal result or conforming exit code; automation must
treat that absence as failure requiring its own recovery policy.

## Secret input and confirmation

An [operator secret input](../CONTEXT.md) is a database-account password read
from exactly one named file or standard input. Password arguments and password
environment variables are unsupported. The source must contain 12–1,024 valid
UTF-8 bytes. One final LF, or one final CRLF, is removed; every other byte,
including whitespace, is preserved. Invalid input fails before work begins.

Secret values never appear in terminal results, progress, diagnostics, logs, or
introspection. A command that does not require a database-account password has
no implicit prompt or ambient credential source.

`shutdown` and `upgrade` may prompt for confirmation only on an interactive
terminal. Non-interactive use requires `--yes`; without either a terminal or
`--yes`, the command fails as `invalid_input` before work begins. No other v0.1
command prompts for confirmation.

## Exact workflow inputs

The following table is the complete command-specific input surface. Options not
listed here or in the server configuration registry are unsupported.

| Command | Required product inputs |
| --- | --- |
| `database init` | `--data-directory PATH`; `--initial-account NAME`; exactly one of `--initial-password-file PATH` or `--initial-password-stdin` |
| `database serve` | Effective closed server configuration, optionally selected by `--config PATH` and overridden by the registry's exact flags |
| `database shutdown` | Online connection inputs below, plus `--yes` for non-interactive confirmation |
| `database backup create` | Online connection inputs below and new `--output PATH` |
| `database backup inspect` | `--backup PATH` |
| `database restore` | `--backup PATH` and a new or empty `--data-directory PATH` |
| `database upgrade` | Offline `--data-directory PATH`, matching pre-upgrade `--backup PATH`, and `--yes` for non-interactive confirmation |
| `database config validate` | The same configuration sources and overrides as `serve` |
| `database data validate` | Offline `--data-directory PATH` |
| `database data inspect` | Offline `--data-directory PATH` |
| `database version` | No command-specific inputs |

Online commands (`shutdown` and `backup create`) use this exact common input
set:

- `--address HOST:PORT` (default `127.0.0.1:3306`);
- required `--account NAME`;
- exactly one of `--password-file PATH` or `--password-stdin`;
- `--tls=disabled|verify-full` (default `disabled`);
- optional `--tls-ca-file PATH` and `--tls-server-name NAME`, valid with
  `verify-full`.

`verify-full` validates trust and server identity. `--tls-ca-file` adds trust
roots and `--tls-server-name` overrides the identity derived from the address.
There is no skip-verification mode. A non-loopback connection with TLS disabled
emits a prominent structured warning. The password source and TLS settings do
not change the terminal result schema.

Ambient server configuration never redirects an offline workflow. In
particular, `restore`, `upgrade`, `data validate`, and `data inspect` use only
their explicit data or artifact paths. `serve` and `config validate` use the
[server configuration registry](server-configuration.md) and its exact source
precedence and validation rules.

## Stable exit classes

Every conforming terminal result has one of these stable classes and process
exit codes:

| Exit code | Class | Meaning |
| ---: | --- | --- |
| `0` | `success` | The workflow reached its successful terminal state. |
| `2` | `invalid_input` | Invocation, input, or effective configuration is invalid. |
| `3` | `precondition` | State, exclusivity, or version compatibility prevents the workflow. |
| `4` | `access` | Connection, authentication, or authorization failed. |
| `5` | `invalid_artifact` | Data or backup is incomplete, corrupt, or untrustworthy. |
| `6` | `operation_failed` | Work began but could not reach its successful terminal state. |
| `7` | `interrupted` | Handled cancellation or termination interrupted the workflow. |

An unhandled runtime or operating-system failure may end without a conforming
result. No exit-class promise survives unconditional process loss.

## Terminal results and diagnostics

The machine-readable terminal envelope is identified by
`database.operator.result/v1`. Its required fields are:

| Field | Meaning |
| --- | --- |
| `schema` | `database.operator.result/v1` |
| `record_type` | `result` |
| `operation_id` | The opaque operation identity for this invocation |
| `command` | Canonical command name, such as `database init` |
| `status` | `success` or `failure` |
| `exit_class` | One stable class from the table above |
| `exit_code` | The corresponding stable process exit code |
| `started_at` | Operation start timestamp |
| `finished_at` | Terminal-result timestamp |
| `duration_ms` | Elapsed operation duration in milliseconds |
| `details` | Command-specific terminal facts |
| `diagnostics` | Zero or more structured diagnostic records |

Every invocation receives one opaque [operator operation identity](../CONTEXT.md).
The same `operation_id` appears in progress, the terminal result, related
structured diagnostics, and server-side operational visibility when the
workflow reaches the server. It correlates records; it is not a durable
command-history identity.

Each diagnostic contains a stable `code`, `severity`, human-readable `summary`,
and optional structured `context`. Codes and context meanings remain compatible
within `0.1.x`; summary wording does not. Authentication diagnostics never
distinguish a missing or suspended account, a wrong password, or insufficient
permission.

Successful command details contain these stable operator facts:

| Command | Required successful `details` facts |
| --- | --- |
| `init` | Instance identity, data directory, initial account, and `stopped` state |
| `serve` | Instance identity, readiness and stopping times, shutdown reason, and final state |
| `shutdown` | Instance identity, request and stopping times, and `stopped` state |
| `backup create` | Artifact path, source identity and version, creation time, backup version, size, and completeness |
| `backup inspect` | Completeness, integrity, source identity and version, creation time, and compatibility |
| `restore` | Artifact path, target directory, new and source identities, data version, and `stopped` state |
| `upgrade` | Identity, directory, previous and resulting data versions, and `stopped` state |
| `config validate` | Validity and every effective setting's redacted value and source |
| `data validate` | Identity, directory, integrity outcome, check time, and structured findings |
| `data inspect` | Identity, versions, compatibility, state, and whether recovery or upgrade is required |
| `version` | Product, build, and platform identity plus data, backup, and named MySQL application compatibility ranges |

Failed artifact-producing commands report whether cleanup is required, whether
any output is usable, and the observable terminal state. An interrupted
`upgrade` reports `upgrade-incomplete` and requires a rerun of the same target
version. Diagnostics and details never expose backup content, account data,
application values, internal file layout, passwords, credentials, or key
material.

## Progress records

Machine-readable progress is emitted as JSON Lines on standard error only when
selected by `--progress=json`. Each record is identified by
`database.operator.progress/v1` and contains:

| Field | Meaning |
| --- | --- |
| `schema` | `database.operator.progress/v1` |
| `record_type` | `progress` |
| `operation_id` | The invocation's operation identity |
| `command` | Canonical command name |
| `sequence` | Monotonically increasing record number |
| `recorded_at` | Record timestamp |
| `phase` | A command-specific stable phase |
| `work` | Optional `completed`, `total`, and `unit` values |

Unknown totals are omitted. Within a phase, completed work never decreases. No
completion time is promised. Progress is non-terminal evidence; only the
terminal result and exit code establish the command outcome.

The closed phase vocabulary is:

| Command | Ordered phases |
| --- | --- |
| `init` | `preflight`, `initializing`, `validating` |
| `serve` | `starting`, `recovering`, `ready`, `stopping` |
| `shutdown` | `connecting`, `requesting`, `draining`, `stopped` |
| `backup create` | `connecting`, `capturing`, `writing`, `validating` |
| `backup inspect` | `reading`, `validating` |
| `restore` | `preflight`, `restoring`, `validating` |
| `upgrade` | `preflight`, `upgrading`, `validating` |
| `config validate` | `loading`, `validating` |
| `data validate` | `preflight`, `validating` |
| `data inspect` | `reading` |
| `version` | No progress; returns its terminal result directly |

## Compatibility and evolution

The result and progress schemas evolve independently. Within `0.1.x`, required
envelope fields, existing field meanings, exit codes, diagnostic codes, and
existing enum meanings remain compatible. Optional fields, diagnostic context,
and enum values may be added. Consumers ignore unknown object fields, preserve
unknown enum values as `unknown`, and retain the original record. An
incompatible schema receives a new identifier rather than silently changing the
meaning of an existing one.

This contract fixes observable operator behaviour only. Command parsing,
process and control-channel structure, credential reading, TLS libraries,
operation execution, progress collection, and record serialization remain
implementation choices so long as these boundaries hold.
