# SQL data and durability

A client connects over the MySQL wire protocol, creates databases and tables,
writes and reads rows, is stopped by constraints when the data would be wrong,
and finds every committed row still there after the server restarts.

## Sub-features

- `sql-ddl` creates and drops databases and tables.
- `sql-use` selects the session's current database.
- `sql-mutations` runs `INSERT`, `UPDATE`, and `DELETE`.
- `sql-select` reads rows, including composed relational queries.
- `sql-constraints` rejects duplicate keys and null values in `NOT NULL` columns.
- `sql-index` serves reads through a declared B-tree index.
- `sql-durability` keeps committed rows across a graceful restart.

## How to get to it (user POV)

- Connect any MySQL client library to the server's `--mysql-listen-address`
  using `caching_sha2_password`, plaintext or with TLS, then send SQL.
- Reach the same data again after `serve` restarts on the same data directory.

## Driving it with control.sh

Preconditions:

- One instance is live for this run and `control.sh doctor <run>` passes.
- No database named `shop` exists yet.

- **Create the schema.** Run
  `control.sh sql <run> 'CREATE DATABASE shop' 'USE shop' 'CREATE TABLE orders (id INT PRIMARY KEY, total INT NOT NULL)'`.
  All three lines report `"ok":true`.
- **Write a row.** Run
  `control.sh sql <run> 'USE shop' 'INSERT INTO orders VALUES (1, 250)' 'SELECT id, total FROM orders'`.
  The `SELECT` line reports `"columns":["id","total"]` and
  `"rows":[["1","250"]]`.
- **Reject a duplicate key.** Run
  `control.sh sql <run> 'USE shop' 'INSERT INTO orders VALUES (1, 9)'`. The
  insert reports `"ok":false` with `"error_code":1062`.
- **Reject a null in a NOT NULL column.** Run
  `control.sh sql <run> 'USE shop' 'INSERT INTO orders VALUES (2, NULL)'`. The
  insert reports `"ok":false` with `"error_code":1048`.
- **Report a missing table.** Run
  `control.sh sql <run> 'USE shop' 'SELECT * FROM missing'`. It reports
  `"ok":false` with `"error_code":1146`.
- **Update and delete.** Run
  `control.sh sql <run> 'USE shop' 'INSERT INTO orders VALUES (2, 100)' 'UPDATE orders SET total = 300 WHERE id = 1' 'DELETE FROM orders WHERE id = 2' 'SELECT id, total FROM orders'`.
  The final read returns exactly `[["1","300"]]`.
- **Confirm the side effect on disk.** Run `control.sh catalog <run>`. The JSON
  has `namespaces.shop.tables.orders` with `columns`, `column_types`, the
  `PRIMARY` constraint, and the stored `rows`.
- **Confirm durability.** Run `control.sh restart <run>`, then
  `control.sh sql <run> 'USE shop' 'SELECT id, total FROM orders'`. The same row
  comes back from the freshly started process.
- **Proof.** Save the mutating statement lines, both reads, the catalog capture,
  and the post-restart read to
  `/tmp/verify-database-evidence/<run>/sql-data-and-durability/`.

## Gotchas

- One `control.sh sql` call is one session. A `USE` in an earlier call is gone;
  repeat `USE shop` at the start of every call, or pass
  `-database shop` to `sqlclient` directly.
- `control.sh sql` exits `1` if any statement failed. That is the expected exit
  for a negative check; read the JSON lines rather than trusting the exit code
  alone.
- Every value is rendered as a string in the `rows` output, and SQL `NULL`
  becomes the literal `"NULL"`. Compare against `"250"`, not `250`.
- `rows_affected` is not populated by this helper. Prove a mutation with a
  follow-up read, not with a row count.
- `INSERT` returning `"ok":true` is not durability proof. Only the on-disk
  catalog or a post-restart read proves the write landed.
- The SQL surface is finite and documented in `docs/mysql-sql-behaviour.md`.
  Before reporting a bug, check that the statement is in scope for v0.1.

