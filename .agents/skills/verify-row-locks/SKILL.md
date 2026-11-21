---
name: verify-row-locks
description: Check row-lock mutual exclusion and deadlock reports when a change touches lock acquire, lock release, or the wait graph in internal/mysql.
---

# Verify row locks

Two sessions never hold conflicting locks on one row, and a wait cycle on locks held now is rolled back. A break lets two sessions change one row, leaves a wait cycle until lock timeout, or rolls back a transaction that can continue.

The question is whether lock events can occur in a bad order. The check is a TLA+ model of each lock critical section, plus the same bound on the real lock manager. `scripts/check-row-locks.sh` pins TLC to tlaplus v1.7.4.

## Steps

1. Compare the change with the covered files below. Stop when the change touches none of them.
   Done when you have named each touched covered function, or you have stopped.

2. From the repository root, run `scripts/check-row-locks.sh`.
   Done when the process has exited and you have kept its output.

3. Read every result with `/statechart`.
   Done when each broken rule has a shortest path in plain words, or the run shows that every rule holds.

4. Match each path to the known findings. Replay any other path on `waitAttempt`, `grant`, `release`, and `lockNowait`.
   Done when a path those functions can perform is recorded as a product bug, and a path they cannot perform is recorded as a modeling error. Repair the model for a modeling error. Keep the known findings.

5. When a change to a covered function is intentional, update `internal/mysql/RowLocks.tla` and `TestRowLockSchedule` so each model step is still one critical section, run the command, and read the result with `/statechart`. Replace the sync commit only after that confirmation.
   Done when the recorded commit is the commit you confirmed and the coverage list still names the functions.

When a later change to the covered files did not rerun this check, use `/maintain-formal-verification-skill`.

## Coverage

Confirmed at commit `62bb34395b5563ae8f5ab3d641c184ccd97bd274`.

- `internal/mysql/locking.go`: `conflictingOwners`, `waitGraphReaches`, `waitAttempt`, `grant`, `lockNowait`, `skipLocked`, `release`
- `internal/mysql/transaction.go`: `statementTransaction.abort`, `releaseSessionLocks`, `rollbackTransaction`, `session.finishTransaction`
- `docs/mysql-sql-behaviour.md`, section "Concurrent row locks"

`AcquireRows` and `RetryWait` are `waitAttempt`. `NowaitRows` is `lockNowait` and `skipLocked`. `ReleaseSession` is `release`, which `abort`, `rollbackTransaction`, and `finishTransaction` call. `release` deletes only that session's grants and its outgoing wait edges. A later `waitAttempt` reads the edges that are still stored.

Bounds: three sessions and one row, shared and exclusive, every reachable state. The model constants are `Sessions = {s1, s2, s3}` and `Rows = {r1}` in `internal/mysql/RowLocks.cfg`. `TestRowLockSchedule` uses the same bound on `lockManager` and also tries `NOWAIT` and `SKIP LOCKED`. The schedule visits more states than TLC because it keeps the last requested mode on an idle session. The lock decision uses that mode only while the session is waiting. The two checks report the same two paths. The file builds only with `-tags rowlockschedule`. The script passes that tag. Ordinary `make test` leaves this schedule out.

Exit 0 means `MutualExclusion`, `JustifiedDeadlock`, and `NoLiveCycle` all hold inside this bound. TLC checks mutual exclusion on its own, then checks the two deadlock rules. TLC stops at the first broken deadlock rule. The schedule test prints both findings. Read both with `/statechart`.

## Known findings

The command exits nonzero on this code. These two paths are the current result. A different path is a new result.

Missed cycle. `NoLiveCycle` breaks. `waitAttempt` leaves both sessions waiting:

1. session 1 takes a shared lock on row 1
2. session 2 takes a shared lock on row 1
3. session 1 waits for an exclusive lock on row 1
4. session 3 takes a shared lock on row 1
5. session 3 waits for an exclusive lock on row 1

Session 1's stored wait set is still session 2. Session 1 and session 3 wait for each other's current shared lock. `waitGraphReaches` sees no stored cycle, so neither session is rolled back. They wait until lock timeout.

False deadlock. `JustifiedDeadlock` breaks. `waitAttempt` returns error 1213 and SQLSTATE 40001:

1. session 1 takes a shared lock on row 1
2. session 2 takes a shared lock on row 1
3. session 1 waits for an exclusive lock on row 1
4. session 2 releases its locks
5. session 2 is told to roll back while waiting for an exclusive lock on row 1

Session 2 no longer holds the row. The stored edge from session 1 to session 2 remains, so the new request is reported as a deadlock. Session 1 could have retried and taken the exclusive lock.

`MutualExclusion` holds for this bound.

## Left out

Lock-wait timeout, cancellation timing, which rows a statement chooses, the rows `SKIP LOCKED` returns, savepoints, and catalog revision conflicts that also return error 1213. An acquire of more than one row, and any bound above three sessions or one row, is outside this check.
