# Query explanation contract prototype

This is a throwaway prototype for the agreed two-layer contract: v0.1 uses a
versioned, machine-readable operator tree as the normative query explanation,
while tabular `EXPLAIN` is a stable, documented MySQL-oriented projection. The
sample deliberately shows a planning choice, its rejected alternative,
estimates, and optional runtime observations so the information lost by the
projection is visible.

At consequential optimizer choices, the canonical form records why the chosen
alternative won and a bounded set of credible rejected alternatives. It does
not expose the optimizer's complete search trace.

Every planned physical operator has a stable ID and tree position and records
its logical purpose, objects and hints, strategy, separated predicate kinds,
row/width/cost/memory estimates, output ordering and uniqueness, choice
rationale, and statistics provenance or evidence warnings.

`EXPLAIN ANALYZE` adds operator invocations; input, output, and filtered rows;
first-row and total timing; peak memory; spills and temporary storage; storage
reads and bytes; waits; estimate divergence; and instrumentation warnings. The
root also separates planning time from execution time. CPU time is not a
mandatory v0.1 measurement.

`EXPLAIN ANALYZE` accepts only `SELECT`, including CTEs and set operations. It
rejects locking reads and mutation before execution, runs the query to
completion under the session's ordinary transaction, isolation, parameter,
timeout, and cancellation semantics, discards result rows, and introduces no
persistent or locking side effects beyond an ordinary read. Plan-only
`EXPLAIN` may describe reads and writes without executing them.

The tabular projection emits one pre-order row per physical operator. It keeps
MySQL's traditional columns as a stable prefix, then appends operator IDs and
kind, cost and memory estimates, nullable runtime measurements, rationale, and
warnings. Plan-only and analyzed output use the same column schema.

The JSON contract carries a format version. Existing fields keep their names,
types, and meanings; compatible releases may add fields, while incompatible
changes use a new version. This is a compatibility guardrail, not a separate
product-version lifecycle.

Run it from the repository root:

```sh
go run ./prototype/query-explanation
```

This prototype is not database implementation code. It uses a fixed in-memory
query and plan; no SQL is parsed or executed.
