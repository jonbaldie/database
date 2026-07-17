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

## Scope boundary

v0.1 does not define a representative application workload. In particular, the
product contract does not invent application fixtures, schemas, row counts,
arbitrary representative queries, or transaction mixes. Its specified
performance-acceptance operations remain required; their exact corpus and
harness mechanics are implementation work. Later design decisions must be
grounded in the documented external database contract rather than an arbitrary
surrogate application.
