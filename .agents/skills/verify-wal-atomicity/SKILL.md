---
name: verify-wal-atomicity
description: Verify crash atomicity when storage replaces table rows, writes transaction WAL records, or replays the WAL at startup. Use when changing the covered catalog or storage paths.
---

# Verify WAL recovery atomicity

**Rule.** Recovery exposes only complete committed transactions and legacy standalone operations. A row replacement exposes all the old rows or all the new rows across every changed table. If `Commit` returns success, restart exposes the new rows.

**Harm.** A partial replacement can erase rows that were committed before the interrupted transaction.

## Coverage and sync

This model follows the transaction marker implementation committed with this version of `WALAtomicity.tla`. Find that sync commit with `git log -1 --format=%H -- .agents/skills/verify-wal-atomicity/WALAtomicity.tla`.

| Code | What the check covers |
| --- | --- |
| `internal/catalog/publish.go` | `stageTableRows`, `replaceAllRows`, `appendRows`, and `preparedRowSync.commit` submit a clear and inserts as one storage transaction. |
| `internal/catalog/rows.go` | Storage transaction calls and row hydration use the recovered engine. |
| `internal/storage/txn.go` | `Transaction.Commit` validates changed overlays under the engine mutex. `appendTransactionWAL` and `writeWALTransaction` write begin, all operations, and commit, then sync before publication. |
| `internal/storage/table.go` | `writeWALRecord` writes length, checksum, and payload separately. Payload decoding accepts both operation codecs and validates marker fields. |
| `internal/storage/wal.go` | `replayWALRecords` owns a local pending transaction. `applyWALOperation` changes rows only for legacy standalone operations or a complete marked transaction. |
| `internal/storage/engine.go` | `Open` returns an error and no engine if replay fails. |
| `internal/storage/checkpoint.go` | The real-code probe checks snapshot publication and WAL rotation after recovery discards an unfinished transaction. |

Before each run, inspect changes to these functions since the sync commit, including uncommitted changes. Follow each changed action in the model and the real-code probe. The model does not read Go source. A passing model alone cannot show that it matches the code. Update the model and probe together when the protocol changes.

## Marker format

The WAL header, checkpoint format, operation kinds 1 through 4, and both operation codecs are unchanged. Begin is kind 5. Commit is kind 6. Each marker uses the versioned payload byte `0xd2`, its kind, empty namespace, empty table name, and zero row fields. The canonical payload is 14 bytes. Its length and CRC frame add 8 bytes. Each changed transaction adds 44 marker bytes.

Unversioned kinds 5 and 6 are invalid. Nonempty marker fields and trailing bytes are invalid. Commit without a pending begin is invalid. A later begin discards an earlier unfinished transaction before it starts a new one. Clean EOF also discards pending operations. Both rules preserve the prior committed rows. Legacy operation-only records still apply immediately outside a marked transaction.

## Run

From the repository root:

```sh
.agents/skills/verify-wal-atomicity/run
```

The command runs TLC 1.7.4 and the real storage code. It needs Java 11 or later, the Go version in `go.mod`, Python 3, `shasum`, and `curl`. On first use it downloads the TLC jar to the user cache and checks its SHA-256. Set `TLA2TOOLS` to a jar with the same checksum to use it offline. The command uses no more than two processors for Go and TLC. It exits with an error if a check fails or cannot run.

Read each TLC result with `/statechart`, using `WALAtomicity.tla`, `WALAtomicity.cfg`, and `WALAtomicityView.js` from this skill. Read the report and any chart before reporting the result. The complete model can exceed the chart's 500-state limit. In that case, a passing run produces a report without a chart.

The real-code probe commits old rows `a,b` in two tables, commits a replacement with only `b`, and opens every byte prefix of the replacement WAL. Every complete-record boundary must open. Before the complete commit marker, every successful open must expose `a,b` in both tables. After the marker, both tables must expose only `b`. Partial frames may refuse to open.

The probe also appends a later transaction after each unfinished complete-record prefix. It checks mixed legacy codecs, checkpoint publication after an abandoned clear, and recovery after WAL rotation. The command includes standard storage tests for malformed markers, unknown operations, semantic replay errors, empty transactions, and both legacy operation codecs. Go loads the extra probe through an overlay. The command does not edit production files.

## Interpret and maintain

Both checks must pass for the marker protocol. The test-only regression commit before the marker fix opens an empty table after the complete clear record. Its expected result is the old rows. This regression was committed before the marker fix. The earlier operation-only model and prefix probe also fail on that boundary.

Compare each result with the covered code and the real-code probe. A model failure needs a real-code reproduction before you report a product bug. For a probe failure, trace the code and update the model if it left out that behavior. Report each new failure and its steps. Keep each rule when it fails. If a fix changes the log format or write order, update the model and probe. Show that the rule fails on broken code. Then run this command and `/statechart` again.

The model covers two tables, legacy standalone operations, one replacement, and one later append transaction. Either writer can stop between record writes, before sync, after sync, or after success. Recovery buffers marked operations until commit and drops an unfinished transaction at EOF or a later begin. The expected rows come from complete writer transactions, independently of replay. The invariant compares the recovered rows with that expected state.

The model assumes a written prefix can survive a process stop before sync. It does not model concurrency, arbitrary transaction histories, checkpoints, semantic row errors, or storage failures beyond torn frames. The real-code tests cover selected checkpoint and malformed-input cases. Neither check proves host power-loss ordering, recovery after arbitrary corruption, or the outcome of a sync error. Recovery memory grows with transaction size. Torn final records can still prevent startup. Older binaries cannot read new marker records. Legacy WAL transactions have no boundaries, so this change cannot recover their missing atomicity guarantees.
