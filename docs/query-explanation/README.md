# v0.1 query explanation contract

This is the published v0.1 query-explanation contract. It defines a product
surface; it is not an internal planner interface. The normative machine form
is a versioned JSON physical-operator tree. The tabular form is a stable
MySQL-oriented projection of the same tree.

## Commands and result forms

- `EXPLAIN [FORMAT=TRADITIONAL|JSON] <statement>` plans a supported `SELECT`, `INSERT`, `REPLACE`, `UPDATE`, or `DELETE` without executing it.
- `EXPLAIN ANALYZE [FORMAT=TRADITIONAL|JSON] <select>` fully executes a non-locking `SELECT`, discards its result rows, and reports complete runtime evidence.
- `EXPLAIN [FORMAT=TRADITIONAL|JSON] FOR CONNECTION <connection_id>` returns a non-blocking partial snapshot of an active plan and its counters.
- `TRADITIONAL` is the default. `FORMAT=JSON` returns one UTF-8 JSON document in one result-set cell; it does not imply a SQL `JSON` value.

Failed or cancelled analysis returns the ordinary statement error instead of a successful partial explanation.

## Public operator vocabulary

The closed v0.1 operator kinds are:

`values`, `scan`, `lookup`, `filter`, `project`, `join`, `aggregate`, `window`, `sort`, `limit`, `distinct`, `set_operation`, `materialize`, `lock`, `constraint_check`, and `mutation`.

An operator kind names developer-recognizable work. Its `operation` block records SQL-visible distinctions. Its optional `strategy` block names the documented execution tactic. New strategies may be added compatibly, but an existing strategy name cannot silently change meaning.

`full_table_scan` reads stored table rows in sequence. `btree_index_scan` traverses one selected logical B-tree index in key order. `btree_covering_index_scan` records that the selected index contains every projected value. The index strategies can be selected by a supported predicate or ordering, or required by a valid MySQL index hint.

All observable write work appears in the tree. Constraint checks, referential actions, and mutations are explicit; `REPLACE` exposes its delete-and-insert behaviour, and cascades are not hidden inside a generic mutation summary.

## JSON document

The exact field and enum contract is in [`explain-v1.schema.json`](./explain-v1.schema.json). Every document has this envelope:

- `format_version`: integer format major, fixed at `1`
- `server_version`: server release that produced the explanation
- `mode`: `plan`, `analyze`, or `snapshot`
- `partial`: `true` only for a live snapshot
- `statement`: statement text, read-only and locking-read classification, and public planning context
- `timing`: planning time and, when execution occurred, elapsed execution time and completeness
- `plan`: the root physical operator
- `warnings`: document-wide structured warnings, always an array
- `snapshot`: connection and capture information, present only in snapshot mode

Submitted SQL is included. Prepared parameters expose only their ordinal position and SQL type; bound values are never echoed. The schema rejects `value`, `bound_value`, `parameter_value`, and `raw_value`; any future parameter field must not contain or derive a bound value. Literal values already present in submitted SQL remain visible. `locking_read` identifies a locking read independently of its SQL text; `EXPLAIN ANALYZE` requires it to be `false`.

Every operator always contains `id`, `kind`, `summary`, `operation`, `estimates`, `output`, `warnings`, and `children`. The optional blocks `strategy`, `objects`, `predicates`, `choice`, `statistics`, `opportunities`, and `actual` appear only when applicable. `summary` is the sole explanatory prose field for the operator; consumers must not parse it as structured data.

JSON children are ordered in execution-input order. Positive integer operator IDs are unique within one document. The nested tree is canonical; `parent_operator_id` is derived only for tabular output.

The physical tree contains only work the statement can execute. An unused CTE
definition therefore has no operator; its submitted text remains source-traceable
through `statement.sql`. A referenced CTE appears once as `materialize` with
reason `cte`; later references identify reuse rather than a second execution.

The JSON Schema defines the structural grammar. These semantic rules complete it where a JSON Schema cannot express a document-wide recursive invariant: plan documents omit `actual` at every node; completed analysis includes `actual` at every node (including a zero-invocation node); snapshots include `actual` precisely for nodes with observed runtime evidence. Producers reject duplicate operator IDs and emit children in execution-input order.

### Absence and nullability

- Required fields remain present, including meaningful zeroes and empty arrays.
- An inapplicable optional block or field is omitted.
- JSON `null` means that the concept applies but the measurement is unavailable. It is used only by applicable runtime measurements such as first-row time or estimate divergence in a partial or never-invoked operator.
- Sentinel values such as `-1`, empty strings, or the string `unknown` are invalid.

### Evidence semantics

- Predicate roles are `access`, `join`, `residual`, and `constraint`. Each predicate carries canonical SQL and the originating SQL construct or constructs.
- Output properties describe projected columns or expressions, guaranteed ordering, and known unique keys.
- Estimates contain output rows, row width, comparative cost, and peak execution memory. Cost is unitless and comparable only within the same server version and planning context; it is not predicted elapsed time.
- Choice evidence appears only at consequential decisions. It contains no more than three credible rejected alternatives and never exposes the complete optimizer search trace.
- Planning opportunities contain a stable code, a concise summary, and evidence. They do not prescribe exact SQL or promise an improvement.
- Runtime elapsed time is inclusive for an operator across all invocations. Parent and child timings may overlap and must not be summed.
- Snapshot counters mean “observed through capture time.” A snapshot never fabricates complete values from partial evidence.

## Tabular projection

Tabular output emits operators in pre-order. These MySQL-compatible columns are the stable prefix:

`id`, `select_type`, `table`, `partitions`, `type`, `possible_keys`, `key`, `key_len`, `ref`, `rows`, `filtered`, `Extra`

These stable columns follow:

`operator_id`, `parent_operator_id`, `operator`, `strategy`, `estimated_cost`, `estimated_memory_bytes`, `actual_rows`, `loops`, `first_row_ms`, `total_ms`, `summary`, `warnings`

Plan-only and analyzed output use the same columns. Inapplicable or unavailable scalar values are SQL `NULL`; warnings are a semicolon-separated list of stable warning codes. Structured predicates, output properties, statistics, choices, rejected alternatives, opportunities, and detailed runtime counters remain JSON-only.

## Evolution

Consumers must ignore unknown object fields. The schema deliberately accepts
unknown object fields so an older validator can accept a version-1 document
with an additive optional field. Adding optional fields, warning or opportunity
codes, or strategies is compatible within format version `1`. Removing fields,
changing field meanings or types, or adding operator kinds requires a new
integer format version. Existing strategy, warning, and opportunity identifiers
cannot silently change meaning.

Counts and byte quantities are non-negative JSON integers. Durations are non-negative decimal milliseconds. Ratios, estimated rows, and comparative costs are non-negative decimals.

## Examples

- [`plan.json`](./examples/plan.json): plan-only read
- [`analyze.json`](./examples/analyze.json): completed read analysis
- [`snapshot.json`](./examples/snapshot.json): partial live plan
- [`write.json`](./examples/write.json): plan-only `REPLACE` with explicit checks and mutations
- [`traditional.txt`](./examples/traditional.txt): stable tabular projection

## Validate

Run `make validate-query-explanation` to validate the four JSON examples with
the pinned AJV draft-2020 tooling. This optional documentation-contract check
requires Node.js and network access for `npx`; the repository-wide `make
quality` gate remains self-contained for its Go toolchain.
