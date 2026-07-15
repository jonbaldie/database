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
