# Query explanation

`EXPLAIN` tells a user how the server will run a statement, and
`EXPLAIN ANALYZE` tells them how it did run. The JSON form is a versioned
public contract (`docs/query-explanation/README.md`), and the traditional form
is the familiar MySQL tabular shape.

## Sub-features

- `explain-traditional` returns the MySQL tabular columns for a statement.
- `explain-json` returns the versioned `format_version` explanation object.
- `explain-analyze` adds observed rows and timing from a real execution.
- `explain-strategy` names the chosen access path and its alternatives.
- `explain-settings` echoes the session settings that shaped the plan.
- `explain-write` explains a mutation as well as a read.

## How to get to it (user POV)

- Send `EXPLAIN <statement>` for the traditional tabular form.
- Send `EXPLAIN FORMAT=JSON <statement>` for the versioned JSON form.
- Send `EXPLAIN ANALYZE <statement>` to run the statement and get observed
  figures.

## Driving it with control.sh

Preconditions:

- One instance is live for this run and `control.sh doctor <run>` passes.
- Database `shop` holds `orders (id INT PRIMARY KEY, total INT NOT NULL)` with
  at least one row.

- **Explain in the traditional form.** Run
  `control.sh sql <run> 'USE shop' 'EXPLAIN SELECT id FROM orders'`. The result
  has the MySQL columns `id`, `select_type`, `table`, `partitions`, `type`,
  `possible_keys`, `key`, `key_len`, `ref`, `rows`, `filtered`, `Extra`, plus
  the product's operator columns `operator_id`, `parent_operator_id`,
  `operator`, `strategy`, `estimated_cost`, and `estimated_memory_bytes`.
- **Explain in the JSON form.** Run
  `control.sh sql <run> 'USE shop' 'EXPLAIN FORMAT=JSON SELECT id FROM orders WHERE id = 1'`.
  The single cell is a JSON document with `"format_version":1`,
  `"mode":"plan"`, `"partial":false`, a `statement` block naming
  `"kind":"select"` and `current_database`, a `timing` block, and a `plan` tree.
- **Check the plan tree.** In that document the `plan` root is
  `"kind":"project"`, its child is `"kind":"filter"` carrying the `WHERE`
  predicate, and its child is `"kind":"scan"` with
  `"strategy":{"name":"btree_covering_index_scan"...}` and
  `"choice":{"selected":"PRIMARY",...}` listing `alternatives`.
- **Check the echoed settings.** The `statement.planning_settings` block reports
  `execution_memory_limit_bytes`, `sql_mode`, `statement_timeout_ms`,
  `temporary_storage_limit_bytes`, and `transaction_isolation`. They match the
  values `SELECT @@statement_timeout_ms` and
  `SELECT @@transaction_isolation` return on the same session.
- **Analyze a real execution.** Run
  `control.sh sql <run> 'USE shop' 'EXPLAIN ANALYZE SELECT id FROM orders'`. The
  result adds observed columns including `actual_rows` and `loops`, and the JSON
  form of the same statement reports `"mode"` as the analyzed mode with
  execution timing.
- **Explain a mutation.** Run
  `control.sh sql <run> 'USE shop' 'EXPLAIN FORMAT=JSON UPDATE orders SET total = total + 1 WHERE id = 1'`.
  The `statement` block reports `"read_only":false`. Confirm afterwards with a
  `SELECT` that a plain `EXPLAIN` did not change the row.
- **Check the contract.** Run `make validate-query-explanation`, which checks
  the committed examples in `docs/query-explanation/examples/` against
  `docs/query-explanation/explain-v1.schema.json`.
- **Proof.** Save the traditional result line, the full JSON explanation, the
  analyzed result, and the schema-validation output to
  `/tmp/verify-database-evidence/<run>/query-explanation/`.

## Gotchas

- The JSON explanation arrives as one string cell inside the `rows` array, so it
  is JSON inside JSON. Pull it out and parse it before asserting; do not match
  substrings against the escaped form.
- `format_version` and the field names are the contract. Cost, row, and memory
  estimates move with the data and are not stable assertions.
- `EXPLAIN ANALYZE` really runs the statement. Never analyze a mutation you do
  not want applied.
- `server_version` inside the explanation can differ from
  `bin/database version` output; do not treat a mismatch as a bug without
  reading `docs/query-explanation/README.md`.
- `partial: true` means the explanation is incomplete for that statement. Report
  it rather than reading the tree as final.

