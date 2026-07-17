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

**v0.1 performance acceptance target**:
A measurable threshold assessed through five independent valid runs on a
published reference environment before v0.1 is released. Four runs must pass;
one miss is published but does not block release, while two misses do. It is a
release-quality gate, not an automated CI test, service-level agreement, or
guarantee that every deployment will achieve the same result.
_Avoid_: Flaky one-run test gate, informational benchmark, universal performance guarantee

**v0.1 capacity envelope**:
The small-application scale at which v0.1 must remain comfortable on one
ordinary machine: approximately 10 GB of stored application data and 50
simultaneous sessions. It is an acceptance boundary for the experimental
release, not a production-scale claim.
_Avoid_: Toy-only database, production-scale capacity guarantee

**v0.1 performance reference environment**:
The specific machine on which v0.1 performance acceptance targets are judged:
an Apple iMac `Mac15,5` with an eight-core M3 chip, 16 GB of memory, and its
internal 512 GB Apple SSD. Each result identifies the operating-system version
and relevant test conditions used for that run.
_Avoid_: Abstract minimum hardware, whichever machine is available

**v0.1 performance acceptance corpus**:
The fixed, versioned, deliberately non-domain relational data used to judge
v0.1 performance targets. It combines narrow keyed records, larger related
records, primary, unique, and secondary access paths, controlled key
distributions, and a 10 GB scale without claiming to represent a particular
application.
_Avoid_: Representative application workload, e-commerce benchmark, implementation test fixture

**v0.1 performance acceptance scenario**:
The versioned release-evidence contract that applies the performance acceptance
corpus to the separate lookup, durable-write, and clean-start gates under fixed
application-visible concurrency, warm-up, measurement, repetition, and
reporting rules. Its normative detail is in
[docs/performance-acceptance.md](docs/performance-acceptance.md).
_Avoid_: Benchmark harness, mixed representative workload, implementation performance test

## Scope boundary

v0.1 does not define a representative application workload. In particular, the
product contract does not invent application fixtures, schemas, row counts,
arbitrary representative queries, or transaction mixes. Its specified
performance-acceptance operations use the fixed, deliberately non-domain
scenario in [docs/performance-acceptance.md](docs/performance-acceptance.md).
The scenario fixes observable corpus, workload, and evidence rules while
leaving harness architecture and internal mechanics as implementation choices.
Later design decisions must be grounded in the documented external database
contract rather than an arbitrary surrogate application.
