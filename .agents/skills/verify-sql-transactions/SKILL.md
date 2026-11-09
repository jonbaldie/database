---
name: verify-sql-transactions
description: Verify transaction begin, commit, rollback, savepoints, one-shot settings, or row-lock changes in the MySQL subsystem.
---

# Verify SQL transactions

Rule: Rollback to a savepoint restores the saved mutation prefix. A finished transaction clears its working state. Begin consumes one-shot settings. Row locks enforce compatibility and reject wait cycles.
Harm: A later commit can include work the user rolled back, or another transaction can inherit stale state.

Run `.agents/skills/verify-sql-transactions/run` from the repository root. It needs Python 3, Java, the Go version from go.mod, and the pinned TLC 1.7.4 jar installed by verify-wal-atomicity. It limits Go to two processors and TLC to one worker. A nonzero exit needs investigation.

Read both SQLTransactions.tla configurations and the delegated lock model results with `/statechart`. Use SQLTransactionsView.js for the transaction flow. Follow the installed `/statechart` instructions to make its files in a new scratch directory.

## Coverage and sync

Source review baseline: fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. Histories 17 and 18 belong to this subsystem.

| Source owner | Modeled transition or probe |
| --- | --- |
| transaction.go, begin and beginTransaction | Enter active state, capture next characteristics, clear prior state |
| transaction.go, consumeNextCharacteristics | Restore session defaults after begin |
| transaction.go, save, rollbackTo, release | Save mutation count and flags, truncate later mutations, retain target savepoint, release it |
| transaction.go, finishTransaction and rollback | Clear transaction state and delegate lock release |
| transaction.go, commitPublishedMutations | Finish a successful commit; the probe checks an empty transaction |
| verify-row-lock-manager | Delegated model and real-code checks for lock compatibility, waits, cycles, release, snapshot restore |

The model has at most two mutations, one savepoint, two read-only choices, and one transaction. Each control action abstracts a synchronous local method; it does not claim concurrent calls on one session are atomic. Read abstracts the read-set latch. Snapshot contents and revisions are outside this first model. The probe also checks a later savepoint and all saved read/dirty flag choices it constructs. The model assumes session default read-only is false; the real probe uses that same default.

## Check the checks

The runner removes mutation-prefix restoration, disables transaction cleanup, and prevents consumption of one-shot settings in separate source overlays. Each must fail with its named assertion. The broken model disables prefix truncation and must violate SavepointRestoresPrefix. The lock skill runs its own broken wait-cycle source overlay. Production files remain unchanged.

Before changing covered code, compare it with the baseline, including working changes. Update model and probe together for intended behavior changes. Move the sync commit only after reviewing source and running this skill. Replay each model failure on real code before reporting a product bug. Use `/maintain-formal-verification-skill` after unverified source changes.

## Snapshot and lock sequences

The same command runs run-sequences. Read SnapshotLifecycle, LockCleanup, and SavepointLocks with their matching configs and View.js files through `/statechart`.

SnapshotLifecycle starts without a captured image. A first read captures it after a possible external publication. A second publication is visible to READ COMMITTED and hidden from REPEATABLE READ. One private write can publish or fail. Resource rejection retains the active stage for retry. Publication rejection clears it. The finite model uses two isolation choices and at most two external revisions; it assumes serialized calls, not arbitrary concurrent schedules. Real catalog probes exercise these paths and read-only mutation rejection.

LockCleanup separates shared acquisition, upgrade wait, cancellation or timeout, exclusive upgrade, extra-row acquisition, failed-read restoration, and release. One owner and two rows are used. The delegated model checks two-owner compatibility and wait graphs. Additional real probes detect conflicting grants, ownership-index disagreement, cycles, retained deadlock edges, retained cancellation/timeout edges, and lost snapshot restoration. The statement-execution skill also checks the actual failed locking SELECT caller.

SavepointLocks separates save, later write/lock, rollback-to, and full rollback. Rollback-to retains its later lock; full rollback releases it. A source overlay that releases locks inside rollbackTo must fail. General duplicate-name behavior and arbitrary savepoint stacks are outside this finite model.

Every configured transaction invariant has a broken model. Source controls cover lazy capture, isolation refresh, publication cleanup, resource rejection, read-only rejection, savepoint prefix/lock retention, one-shot consumption, finish cleanup, and the lock properties above. The runner requires named assertions, then reruns current source. No build error counts as negative evidence. These are finite source-linked checks, not a proof of isolation for every SQL program.
