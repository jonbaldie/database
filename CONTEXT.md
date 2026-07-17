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

## Scope boundary

v0.1 does not define a representative application workload. In particular, the
product contract does not invent application fixtures, schemas, row counts,
arbitrary representative queries, or transaction mixes. Its specified
performance-acceptance operations remain required; their exact corpus and
harness mechanics are implementation work. Later design decisions must be
grounded in the documented external database contract rather than an arbitrary
surrogate application.
