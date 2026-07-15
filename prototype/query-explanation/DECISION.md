# Validated v0.1 query explanation contract

The v0.1 contract has two layers: versioned JSON is normative and lossless,
while tabular output is a stable, documented MySQL-oriented projection.

The canonical form is a physical operator tree. Every planned operator exposes
a stable ID and tree position, logical purpose, referenced objects and hints,
strategy, separated access/join/residual predicates, row/width/cost/memory
estimates, output ordering and uniqueness, choice rationale, a bounded set of
credible rejected alternatives, statistics provenance, and evidence warnings.
The complete optimizer search trace remains internal.

`EXPLAIN ANALYZE` additionally reports invocations; input, output, and filtered
rows; first-row and total elapsed time; peak memory; spills and temporary
storage; logical and physical reads and bytes; lock and other waits; estimate
divergence; and instrumentation warnings. Planning and execution totals are
separate. CPU time is not mandatory in v0.1.

Plan-only `EXPLAIN` accepts supported reads and writes without executing them.
`EXPLAIN ANALYZE` accepts only `SELECT`, including CTEs and set operations, and
rejects locking reads or mutations before execution. It fully executes under
the current session's ordinary transaction, isolation, parameters, timeout,
and cancellation semantics while discarding result rows.

The tabular form emits one pre-order row per physical operator. MySQL's
traditional columns are a stable prefix; operator identity, cost, memory,
nullable runtime values, rationale, and warnings follow. Plan-only and analyzed
output share the same columns.

Existing JSON fields retain their names, types, and meanings. Compatible
releases may add fields; incompatible changes use a new format version.

`EXPLAIN FOR CONNECTION` takes a non-blocking snapshot of an active plan and
its counters, marks the result partial, and errors for an idle or missing
connection. Authorization is specified with the server/authentication boundary.
Failed or cancelled analysis returns the ordinary MySQL error, not a misleading
successful or partial explanation. Concrete operator kinds are specified by
the downstream planning and execution decisions.
