---
name: verify-row-lock-manager
description: Check row-lock compatibility and deadlock prevention when changing the MySQL row lock manager or its transaction cleanup paths.
---

# Verify row-lock safety

**Rule.** A row cannot have an exclusive lock and another session's lock at the same time. The wait graph cannot contain a cycle. A request that would create a cycle returns a deadlock error and does not leave a wait edge behind.

**Harm.** A broken lock rule can let two sessions change the same row at once, or leave both sessions waiting until timeout.

## Coverage and sync

The model describes the lock manager's wait-policy protocol. The Go probe calls the real manager and also checks multi-row requests, `NOWAIT`, `SKIP LOCKED`, release, and lock snapshot restore.

The verification was checked against code at commit `70126009c8ee92d8b3183d1e653a9812f0bfa4fb`.

| Code | What the check covers |
| --- | --- |
| `internal/mysql/locking.go` | `conflictingOwners`, `waitAttempt`, `waitGraphReaches`, `grant`, `release`, `snapshot`, and `restore`. |
| `internal/mysql/locking_read.go` | The lock modes and policies passed to the manager. The check does not parse SQL or build row resources. |
| `internal/mysql/relational_select.go` | Restore of the lock snapshot when a locking read fails. The Go probe checks the manager snapshot and restore methods directly. |
| `internal/mysql/transaction.go` | Lock release after commit and after deadlock or cancellation. The check covers `release`; it does not model the full SQL transaction path. |

Before each run, inspect changes since the sync commit, including uncommitted changes, to the listed functions. Update the model and Go probe together when their protocol changes. Move the sync commit forward only after confirming that the model and probe still describe the code at the new commit.

## Run

From the repository root:

```sh
.agents/skills/verify-row-lock-manager/run
```

The command runs TLC 1.7.4 and a Go probe with the Go version in `go.mod`. It also runs the probe against a temporary broken copy of `waitGraphReaches`. That check must fail because it does not reject a cycle. The command reports an error if the broken copy passes. It does not edit production files.

The first run needs Java 11 or later, Python 3, `shasum`, `curl`, and network access to download TLC. The script checks the TLC jar against its pinned SHA-256. Set `TLA2TOOLS` to a jar with that checksum to run offline. The command uses no more than two Go processes and one TLC worker.

Read every TLC result with the pstack `/statechart` skill. Use `RowLockManager.tla`, `RowLockManager.cfg`, and `RowLockManagerView.js` from this skill. The statechart report gives the shortest path to each broken rule. When the model passes, it draws the checked states and transitions.

## Model and real-code check

The model checks two sessions and one row. Either session can request a shared or exclusive lock, wait on a conflicting owner, or release all its locks. Each model action represents one lock-manager step while its mutex is held. TLC checks every order of those steps in this bound.

The model checks these rules:

- `NoConflictingGrants`: one session's exclusive row lock excludes every other session's lock on that row.
- `LockIndexesMatch`: the `held` map and the `granted` map agree about each owner and row.
- `WaitGraphAcyclic`: wait edges never form a cycle.
- `DeadlockResponseClearsWait`: a deadlock response leaves no wait edge for that session.

The real-code probe also checks shared-lock compatibility, exclusive-lock conflicts, an upgrade deadlock, all-or-nothing multi-row requests, `NOWAIT`, `SKIP LOCKED`, release, and snapshot restore. A mutation check replaces `waitGraphReaches` with a function that always returns false. The cycle test must catch that break.

## Interpret and maintain

If TLC reports a broken rule, read its shortest path with `/statechart` and describe each request and release using the two sessions and the row. Replay that path against the real Go manager. If the Go probe does not reproduce it, check the model against `locking.go` before you report a product bug.

If the real-code probe fails, trace the Go state maps and SQL failure code. Keep each rule as written. Do not weaken the model to make it pass. Do not change product code unless the user asks. Update the model, probe, and coverage table when the implementation changes. Then run this command and `/statechart` again.

The model does not cover multiple rows, SQL parsing, row selection, lock fairness, scheduler timing, wait timeouts, cancellation, catalog revision conflicts, or the full transaction lifecycle. The Go probe covers selected multi-row and cleanup behavior. Neither check proves that every SQL locking read is correct or that a waiting session will eventually acquire a lock.
