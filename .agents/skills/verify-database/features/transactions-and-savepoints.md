# Transactions and savepoints

A client groups statements into a transaction, undoes part of it with a
savepoint, commits the rest, and finds an open transaction rolled back when its
session or the server ends. A second session that wants a locked row waits,
fails with a documented lock error, or is refused immediately with `NOWAIT`; it
never writes over the held row.

## Sub-features

- `txn-commit` makes every statement in a committed transaction visible.
- `txn-rollback` discards an explicit `ROLLBACK`.
- `txn-savepoint` undoes back to a savepoint and keeps the earlier work.
- `txn-isolation` hides uncommitted work from another session
  (`REPEATABLE-READ`).
- `txn-nowait` refuses `FOR UPDATE NOWAIT` against a held row with `3572`.
- `txn-lock-wait` times out a blocked writer with `1205`.
- `txn-deadlock` breaks a cycle of waiting writers with `1213`.
- `txn-stop-rollback` rolls back an open transaction when `serve` stops.

## How to get to it (user POV)

- Send `BEGIN`, `COMMIT`, `ROLLBACK`, `SAVEPOINT <name>`,
  `ROLLBACK TO SAVEPOINT <name>`, and `RELEASE SAVEPOINT <name>` on a client
  session.
- Send `SELECT ... FOR UPDATE` or `SELECT ... FOR UPDATE NOWAIT` for a locking
  read.
- Open a second client session against the same server for a concurrent write.
- Stop the server while a transaction is open.

## Driving it with control.sh

Preconditions:

- One instance is live for this run and `control.sh doctor <run>` passes.
- Database `shop` holds table `orders (id INT PRIMARY KEY, total INT NOT NULL)`
  with rows `(1, 250)` and `(2, 100)`.

- **Commit a transaction.** Run
  `control.sh sql <run> 'USE shop' 'BEGIN' 'INSERT INTO orders VALUES (3, 99)' 'COMMIT' 'SELECT id FROM orders'`.
  The read returns `[["1"],["2"],["3"]]`.
- **Roll back a transaction.** Run
  `control.sh sql <run> 'USE shop' 'BEGIN' 'INSERT INTO orders VALUES (4, 5)' 'ROLLBACK' 'SELECT id FROM orders'`.
  Row `4` is absent.
- **Undo to a savepoint.** Run
  `control.sh sql <run> 'USE shop' 'BEGIN' 'INSERT INTO orders VALUES (4, 40)' 'SAVEPOINT s1' 'INSERT INTO orders VALUES (5, 50)' 'ROLLBACK TO SAVEPOINT s1' 'COMMIT' 'SELECT id FROM orders'`.
  Row `4` is present and row `5` is absent.
- **Hold a lock.** Start the holder in the background and give it time to take
  the lock:

  ```sh
  control.sh sql <run> --hold 6s 'USE shop' 'BEGIN' 'UPDATE orders SET total = 1 WHERE id = 1' > /tmp/holder.log 2>&1 &
  sleep 2
  ```

  `/tmp/holder.log` shows `BEGIN` and the `UPDATE` both `"ok":true`.
- **Refuse a locking read.** While the holder waits, run
  `control.sh sql <run> 'USE shop' 'SELECT id FROM orders WHERE id = 1 FOR UPDATE NOWAIT'`.
  It fails at once with `"error_code":3572`.
- **Confirm isolation.** While the holder still waits, run
  `control.sh sql <run> 'USE shop' 'SELECT id, total FROM orders WHERE id = 1'`.
  It returns the committed `[["1","250"]]`, not the holder's uncommitted `1`.
- **Confirm the holder rolls back.** Run `wait`, then
  `control.sh sql <run> 'USE shop' 'SELECT id, total FROM orders WHERE id = 1'`.
  The value is still `250`, because the holder's session closed with the
  transaction open.
- **Time out a blocked writer.** Hold the lock again with `--hold`, then run a
  plain `UPDATE orders SET total = 2 WHERE id = 1` from a second call. It either
  succeeds after the holder releases or fails with `"error_code":1205`. Record
  which, with the timing.
- **Roll back on stop.** Start a holder with a long `--hold` that inserts row
  `7`, run `control.sh restart <run>`, then read the table. Row `7` is absent
  and every committed row is present.
- **Proof.** Save every statement line for each scenario, the holder log, and a
  `control.sh catalog <run>` capture after the commit and after the restart to
  `/tmp/verify-database-evidence/<run>/transactions-and-savepoints/`.

## Gotchas

- There is no `SLEEP` function; `SELECT SLEEP(1)` fails with `1305`. Hold a
  session open with `control.sh sql <run> --hold <duration>`, which sleeps after
  the last statement while the session, and therefore the transaction, stays
  open.
- One `control.sh sql` call is one session on one connection. A transaction
  cannot span two calls; a concurrency scenario needs two calls, one of them
  backgrounded with `&`.
- Always `wait` for a backgrounded holder before the next scenario or teardown.
  A stranded holder makes the next read contend for a locked row.
- A holder that ends without `COMMIT` rolls back. That is useful for lock tests,
  but it means a `--hold` call is never a way to write data.
- The default isolation is `REPEATABLE-READ`; confirm with
  `SELECT @@transaction_isolation`. The default statement timeout is
  `SELECT @@statement_timeout_ms` milliseconds, so an apparent hang may be the
  documented timeout arriving as `1205`.
- Rolling back to a savepoint does not end the transaction. `COMMIT` or
  `ROLLBACK` is still needed.
- `control.sh sql` exits `1` when any statement fails. For lock tests that is
  the expected exit; read the `error_code` rather than the exit code.

