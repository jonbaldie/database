---
name: verify-storage-recovery
description: Check bounded storage commit, recovery, checkpoint, schema, hydration, catalog publication, index cache, and allocation transitions. Check the stated scope before using this partial subsystem skill.
---

# Verify storage commit and recovery

An acknowledged row must survive a process restart. An incomplete marked transaction must not change recovered rows. A closed engine must reject a new transaction and an empty commit.

Run from the repository root.

```sh
.agents/skills/verify-storage-recovery/run
```

The command needs Python 3, Java 11 or later, and the Go toolchain in go.mod. It uses the checksum-pinned TLC 1.7.4 jar installed by verify-wal-atomicity. Run that skill first if the jar is absent. Set TLA2TOOLS to the same jar to use another location. Go and TLC use at most two processors.

Read every model result with `/statechart`. Use CommitRecovery.tla, CommitRecovery.cfg, and CommitRecoveryView.js. Read the delegated WAL model result with `/statechart` too.

## Source and sync

Last source review is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. Before use, inspect later and uncommitted changes in these functions. Move the sync commit forward only after the model and probes match the changed code.

| Model action or check | Source |
| --- | --- |
| WriteBegin, WriteOperations, WriteCommit | internal/storage/txn.go, writeWALTransaction and writeOverlay |
| Sync | internal/storage/txn.go, appendTransactionWAL |
| Publish, ReturnSuccess | internal/storage/txn.go, Transaction.Commit and publishCommittedOverlays |
| Recover | internal/storage/engine.go, Open; internal/storage/wal.go, replayWALRecords and pendingWALTransaction.commit |
| Closure probe | internal/storage/engine.go, Begin and Close; internal/storage/txn.go, ensureCommitReady |
| Byte-prefix recovery and selected checkpoint probes | Delegated .agents/skills/verify-wal-atomicity/SKILL.md |

## Evidence and bounds

CommitRecovery models one changed transaction, two abstract row values, complete log-record writes, and one process stop at any stage. It assumes successful writes can survive a process stop before sync. Sync succeeds. It does not model power loss or disk write reordering.

The source probe commits one row in one table, checks private and published visibility, rejects a repeated commit, closes the engine, and reopens it. A separate probe checks closure and repeated close. The runner changes the final commit marker to a begin marker in a temporary Go overlay. The restart assertion must fail because replay discards the pending transaction. A second overlay removes the closed-engine guard. The empty-commit assertion must fail. A third overlay applies a pending operation before its commit marker. A separate incomplete-log probe must reject the recovered row. Compiler errors do not count as caught mutations.

The runner also runs verify-wal-atomicity. That skill checks two tables and all byte prefixes of one replacement transaction, selected legacy records, malformed markers, and checkpoint cases. Read its limits before reporting coverage.

Histories 6 through 13 each have a bounded check. EngineLifecycle now models closure and transaction finish/rejection states as a separate sequence. Concurrent transactions, arbitrary recovery histories, power loss, and general write failures remain outside these checks.

## Interpret failures

A current-model or current-source failure stops the check. The broken models must violate AcknowledgedRowsSurviveRestart and UncommittedRowsStayHidden. Each broken-source run must fail at its named behavioral assertion. Each model rule has a separate broken-source check. The delegated prefix probe also checks UncommittedRowsStayHidden at every tested byte prefix.

Keep findings visible. Reproduce a model failure against source before reporting a product defect. If the code changed intentionally, update the model and probe together, demonstrate the broken variant, then run this skill and `/statechart` again. Use `/maintain-formal-verification-skill` after covered code changes without these checks.

## Storage detail checks

The same command runs run-details. Each model has a matching config and View.js file. Read each current and broken result with `/statechart`.

| History | Model | Source boundary | Bound and checked rule |
| --- | --- | --- | --- |
| 8 | CheckpointPublication | internal/storage/checkpoint.go, Checkpoint and WAL publication | One committed row image, one checkpoint, one process stop. Snapshot publication precedes log replacement. Failed rotation fences the writer. |
| 9 | SchemaVersion | internal/storage/txn.go, validateOverlays; schema.go | One staged insert and one schema version change. A stale transaction cannot publish. |
| 10 | CatalogHydration | internal/catalog/rows.go, hydrate rows | One legacy image and one durable image. Durable rows take priority. |
| 11 | CatalogRevision | internal/catalog/writer.go, revision claim | Two replacement requests for one revision. At most one succeeds. |
| 12 | OrderedCache | internal/catalog/ordered_index.go, cache reuse | One cached row image and one changed image. The index must match the new image. |
| 13 | AutoIncrement | internal/mysql/auto_increment.go, advance and allocate | Positive identifiers 1 through 3, at most three steps. Future generated values exceed recorded values unless allocation is exhausted. |

The real storage probe injects a stop error at each of four checkpoint hooks, closes and reopens the engine, and checks the committed row. This is a controlled return and restart, not an operating-system kill or power loss. A separate directory-sync failure must reject the next transaction. A same-width schema rename must reject a staged insert; adding a column must pad and reindex the prior row.

Catalog probes check durable-image selection, same-revision and stale-revision rejection, cache reuse for unchanged rows, invalidation for changed rows, and preservation of the old snapshot. They call the real helpers sequentially. They do not prove writer scheduling or concurrent cache access.

Allocation probes generate all 255 unsigned tinyint values, reject the next allocation, and check that the failed insert leaves rows unchanged. They also check advance/exhaust behavior for all positive values within limits 1 through 255. Signed types, arbitrary integer widths, persistence across restart, and concurrent allocation are outside this check.

Eight broken configs and eight source overlays must fail the named detail rules or assertions. Source faults omit snapshot publication, writer fencing, version validation, hydration, revision claims, row-image identity checks, identifier advance, or exhaustion. The later sequence models below extend table operations, catalog publication, and allocator initialization. Arbitrary indexes, SQL expression semantics, and general metadata write failures remain outside these bounded models.

## Full bounded storage sequences

The same command also runs run-sequences. These models extend the earlier guard checks. They use one table, one catalog batch, or one allocator. Read each matching model, config, and View.js result with `/statechart`. Current charts and reports are in docs/formal/storage-recovery/sequences. Intentional broken controls are in sequence-controls.

| History | Model | Source actions | Property and bound |
| --- | --- | --- | --- |
| 9 | TableSchema | internal/storage/meta.go, EnsureTable, rebuildIndexes, persistMetaLocked, DropTable; engine.go, Open | Create one table, reshape it, drop it, and recreate its name. Metadata write, sync, rename, and directory sync are separate stages. A successful shape change has matching persisted metadata. Dropped rows must not reappear after recreation and restart. |
| 11 | CatalogPublication | internal/catalog/writer.go, writeLoop, collectWriteBatch, applyWriteRequests, commitWriteBatch, publishWriteBatch; publish.go, preparedRowSync.commit; catalog.go, persistLocked | One batch with one abstract accepted change, or all requests rejected. Queue collection, isolated apply, validation, row preparation, row sync, schema checkpoint, schema persistence, revision publication, and notification are separate steps. Success requires row and schema publication. Failure does not publish a revision. |
| 13 | AllocationLifecycle | internal/mysql/auto_increment.go, initializeAutoIncrementState, fillAutoIncrementValue, advanceAutoIncrement, resetAutoIncrementState; ddl.go, truncateTable | Disabled or uninitialized allocation, stored-value scan, generated and explicit values, exhaustion, and truncation. Values are zero through three. At most three insert or truncate steps follow initialization. A usable next value exceeds stored values. A disabled allocator has no state. Truncation resets next to one. |

TableSchema explicitly allows in-memory changes before metadata persistence. A failed metadata operation can leave memory different from disk. The model does not assert rollback on such an error. A real probe blocks the metadata temporary path and checks the returned error, changed memory, and unchanged metadata file. The normal probe checks creation, row insertion, reshape, rebuilt lookup, idempotent ensure, and drop. A separate drop/recreate/restart probe rejects historical rows from the old table. It uses successful filesystem operations except for the stated injected failure.

The catalog probe puts two requests into the real queue and runs the real writer loop. It checks one collected batch and one published revision. A wrapper around the real storage engine checks that no waiter is notified and no revision is published when row commit begins. The probe checks schema persistence before success and reopens the catalog to check both rows. Apply, validation, and schema-file failures must report errors and retain the prior visible revision. Schema persistence failure can leave rows durable while the visible catalog remains old. That intermediate state is intentional in the model. Row storage and schema persistence are not one atomic transaction.

The allocation source probe clears stale state on a table with allocation disabled. It scans empty rows, mixed stored identifiers, and the unsigned tinyint maximum. Repeated initialization must retain initialized state. A SQL sequence inserts the maximum identifier, truncates the table, then generates identifier one. The source probe uses the existing test fixture only to create a catalog and executor. The SQL operations and allocation functions are production functions.

There are nine broken sequence configs and nine source overlays. They omit metadata persistence, the drop checkpoint, row commit, schema persistence, notification ordering, failure reporting, disabled-state clearing, stored-value scanning, or truncate reset. Each must fail its named invariant or behavioral assertion. The runner then repeats all three current package probes. Build errors never count as detected faults.

These are sequence models at finite bounds. TableSchema does not model all tables, shrinking-column conversions, every metadata error, or power loss. CatalogPublication assumes one serialized writer and does not model the timer scheduler, arbitrary callbacks, partial multi-table row commits, all row-sync failures, or later recovery from a row/schema mismatch. AllocationLifecycle abstracts a stored-value scan as one step and excludes concurrent allocation, every numeric type, and all DDL changes. The previous stale-schema, revision-claim, cache, and allocation-value checks remain in the same skill.

## Engine and transaction lifecycle

The run command also invokes run-engine. Read EngineLifecycle and its five broken configs with `/statechart`, using EngineLifecycleView.js. The model separates active, staged, failed-but-unfinished, committed-and-finished, repeat rejection, engine closure, and commit/begin rejection after close. It uses one transaction, one selected validation failure, and successful or closed-engine calls. WAL details stay in CommitRecovery and the delegated WAL skill.

Five source controls mark a failed transaction finished, omit successful finish, allow repeated commit, allow closed-engine commit, or allow closed-engine begin. Each must fail its named behavioral assertion. The current source probe uses one row and a stale schema version for validation failure. It also checks repeated Close. Current source is rerun after all faults. This does not prove every failure path, all caller combinations, or concurrent Close/Commit.
