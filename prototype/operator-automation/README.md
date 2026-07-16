# Prototype: v0.1 operator automation contract

> THROWAWAY DECISION PROTOTYPE — this explores a public product contract. It is
> not implementation guidance or final documentation.

## Question

Which exact command inputs, secret-supply methods, progress records, exit
classes, and versioned machine-readable result schemas make every supported
operator workflow reliably automatable?

The prototype makes one candidate contract concrete enough to inspect. It
deliberately stays at the observable command boundary: parsers, process
structure, control channels, file formats, and workflow algorithms are not part
of it.

## Run

From the repository root:

```sh
go run ./prototype/operator-automation/main.go ./prototype/operator-automation/contract.go
```

Use the keys shown at the bottom of the screen to move through commands,
success and failure cases, progress records, and final results.

## Candidate common contract

- `--result human|json` controls the single terminal result written to standard
  output; the default is `human`.
- `--progress auto|human|json|none` controls progress on standard error. `auto`
  means human progress on a terminal and no progress otherwise.
- JSON progress is newline-delimited. It never shares standard output with the
  final JSON result.
- `shutdown` and `upgrade` prompt only on an interactive terminal. `--yes` is
  the exact non-interactive acknowledgement; absence of either a terminal or
  `--yes` is invalid invocation.
- Passwords are accepted only from a named file or standard input, never from
  an argument, environment variable, prompt in non-interactive use, result,
  progress record, or diagnostic.
- A password source contains 12–1024 UTF-8 bytes. One final LF, or CRLF, is
  removed; no other whitespace is changed.
- Every command emits at most one terminal result. A successful result means
  the documented workflow reached its terminal state. Progress, request
  acceptance, or reaching an intermediate phase is not success.

## Stream contract

| Stream | Human mode | Machine mode |
| --- | --- | --- |
| Standard output | One terminal summary | Exactly one `database.operator.result/v1` JSON object |
| Standard error | Optional progress and diagnostics | Optional `database.operator.progress/v1` JSON Lines records |

Abrupt process loss can leave progress without a terminal result. Automation
therefore treats only the result object plus process exit code as terminal.
There is no `complete` progress phase. For `serve`, reaching `ready` is a
progress event; a successful terminal result arrives only after a graceful
stop.

If `--result=json` is recognizable, invalid invocation and validation outcomes
also use the machine-readable result. An invalid result-format selection or a
failure before command processing may use ordinary error output.

## Exact exit classes

| Code | Stable class | Meaning |
| ---: | --- | --- |
| 0 | `success` | The requested workflow reached its documented terminal state |
| 2 | `invalid_input` | Invocation, input, or effective configuration is invalid |
| 3 | `precondition` | Current state, exclusivity, or version compatibility prevents the workflow |
| 4 | `access` | Connection, authentication, or authorization failed |
| 5 | `invalid_artifact` | Validation found corruption, incompleteness, or an untrustworthy artifact |
| 6 | `operation_failed` | The workflow began but could not reach its successful terminal state |
| 7 | `interrupted` | A handled cancellation or termination interrupted the workflow |

An unhandled runtime or operating-system failure may end without a conforming
result. No product can promise an exit code after unconditional process loss.

## Machine-readable records

Every invocation receives one opaque **operator operation identity**. The same
`operation_id` appears in progress, the terminal result, structured diagnostics,
and server-side operational visibility when the workflow reaches the server.
It does not create permanent command history.

`database.operator.result/v1` contains `record_type`, `operation_id`, `command`,
`status`, `exit_class`, `exit_code`, `started_at`, `finished_at`, `duration_ms`,
command-specific `details`, and `diagnostics`.

`database.operator.progress/v1` contains `record_type`, `operation_id`,
`command`, a monotonically increasing `sequence`, `recorded_at`, a stable
command-specific `phase`, and optional `work` with `completed`, `total`, and
`unit`. Unknown totals are omitted, completed work never decreases within a
phase, and no completion time is promised.

Diagnostics contain a stable `code`, `severity`, human-readable `summary`, and
optional structured context. Codes and context meanings are stable within
`0.1.x`; summary wording is not. Authentication diagnostics never distinguish
a missing or suspended account, wrong password, or insufficient permission.

Failed artifact-producing commands report whether cleanup is required, whether
any output is usable, and the observable terminal state. Interrupted upgrade
reports `upgrade-incomplete` and requires a same-target rerun.

## Stable progress phases

| Command | Ordered phase vocabulary |
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

## Connection security

Online commands use `--address HOST:PORT` (default `127.0.0.1:3306`), a required
`--account`, one operator secret input, and `--tls=disabled|verify-full`
(default `disabled`). `verify-full` validates trust and server identity;
`--tls-ca-file` adds trust roots and `--tls-server-name` overrides the identity
derived from the address. There is no skip-verification mode. Disabling TLS for
a non-loopback address emits a prominent structured warning.

## Confirmation

`shutdown` and `upgrade` may prompt only when attached to an interactive
terminal. Non-interactive use requires `--yes`; otherwise the command fails as
`invalid_input` before beginning work. No other v0.1 command prompts for
confirmation.

## Secret interpretation

An operator secret input contains 12–1024 valid UTF-8 bytes. One final LF or
CRLF is removed and every other byte, including whitespace, is preserved.
Invalid, empty, or oversized input fails before work begins.

## Evolution rule

Within `0.1.x`, required envelope fields, existing field meanings, exit codes,
diagnostic codes, and existing enum meanings do not change. Optional fields and
new enum values may be added; consumers must ignore unknown fields and handle
unknown enum values as `unknown`, while retaining the original record.

## Settled through reaction

- Passwords use an **operator secret input**: a named file or standard input
  only. Command arguments and environment variables are unsupported.
- Non-interactive automation receives machine-readable **operator command
  progress** only when it explicitly requests `--progress=json`. The default is
  quiet when standard error is not a terminal.
- Connection security, exact common and command-specific inputs, confirmation,
  stream and result semantics, exit classes, progress and diagnostic schemas,
  partial-output reporting, correlation, and schema evolution use the contract
  above.

## Verdict

Accepted: the candidate is the v0.1 operator automation contract.
