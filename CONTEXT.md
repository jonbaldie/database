# Database

This context describes a relational database built for application developers who need to understand and test the behaviour of their queries.

## Language

**Application developer**:
The primary user: a developer who writes database queries as part of an application and tests their behaviour before release.
_Avoid_: DBA, analytics engineer

**Query behaviour**:
The observable relationship between a query, its chosen execution, its results, and its performance characteristics. It is expected to be inspectable, explainable, and reproducible in application tests.
_Avoid_: Black-box performance

**Query explanation**:
A stable, machine-readable and human-readable account of how the database planned and executed a query, including both its choices and observed operator-level behaviour.
_Avoid_: Query log, debug text

**Database account**:
An authenticated identity used by an application or operator to establish
sessions and exercise permissions. It is distinct from a session, which is one
live connection.
_Avoid_: User, login

**Account name**:
The case-sensitive, server-wide identifier of one database account. It has no
host component; MySQL-style `name@host` identities are unsupported.
_Avoid_: Host-qualified user, login name

**Account-administration SQL contract**:
The finite MySQL-shaped v0.1 commands, authorizations, account lifecycle, and
introspection rules for database accounts and grants. Its normative detail is
in [docs/account-administration.md](docs/account-administration.md).
_Avoid_: General MySQL user administration, implicit account creation

**Server configuration registry**:
The closed catalog of v0.1 server startup settings, with canonical names,
value types, defaults, allowed values, secret treatment, and corresponding
file, environment-variable, and command-line forms. It excludes session
settings and implementation-only tuning controls.
_Avoid_: Open-ended configuration, hidden tuning knobs, session settings registry

**Session settings registry**:
The closed catalog of application-facing settings owned by one live database
session, including their defaults, allowed values, and reset behaviour. It may
tighten applicable server configuration registry safeguards but cannot disable
or exceed them.
_Avoid_: Server configuration registry, open-ended session variables

**Operator command family**:
The single supported `database` executable through which operators initialize,
serve, shut down, create and inspect backups, restore, upgrade, validate
configuration and data, inspect data, and report version for a v0.1 database
server. Its subcommands form one coherent product interface without making the
executable's internal organization part of the contract.
_Avoid_: Separate server and administration programs, internal component interface

**Operator command result**:
The terminal success or explicit-failure outcome of one operator command,
available as human-readable output and through the versioned
`database.operator.result/v1` form. Its meanings remain compatible within
`0.1.x`; progress is observable but is not itself a successful result.
_Avoid_: Log wording as API, command acceptance as completion, unstructured exit failure

**Operator command exit class**:
One of the stable automation outcomes `success`, `invalid_input`,
`precondition`, `access`, `invalid_artifact`, `operation_failed`, or
`interrupted`, paired with its documented process exit code. Unconditional
process loss may prevent the command from reporting any conforming class.
_Avoid_: Error-message parsing, one undifferentiated failure code, signal guarantee after process loss

**Operator command progress**:
Optional non-terminal evidence about the current phase and measurable work of
an operator command. Non-interactive automation receives versioned
machine-readable progress only when it explicitly requests it; progress never
establishes a successful result.
_Avoid_: Completion result, default automation noise, permanent command history

**Operator secret input**:
A database-account password supplied to an operator command from a named file
or standard input, never through a command argument or environment variable.
Its value is absent from command results, progress, and diagnostics.
_Avoid_: Password flag, password environment variable, operator credential store

**Operator operation identity**:
An opaque identifier correlating one operator-command invocation across its
progress, terminal result, structured diagnostics, and server-side operational
visibility where applicable. It is not a durable command-history identity.
_Avoid_: Server instance identity, process identity, permanent audit record

**Operator automation contract**:
The normative v0.1 command inputs, secret and confirmation rules, streams,
progress records, terminal result schemas, diagnostics, exit classes, and
compatibility rules for automating the operator command family. Its detail is
in [docs/operator-automation.md](docs/operator-automation.md).
_Avoid_: CLI help text as API, progress as completion, implicit credential input

## Scope boundary

v0.1 does not define a representative application workload. In particular, the
product contract does not invent application fixtures, schemas, row counts,
arbitrary representative queries, or transaction mixes. Its specified
performance-acceptance operations remain required; their exact corpus and
harness mechanics are implementation work. Later design decisions must be
grounded in the documented external database contract rather than an arbitrary
surrogate application.
