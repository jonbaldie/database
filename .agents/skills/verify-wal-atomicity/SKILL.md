---
name: verify-wal-atomicity
description: Verify crash atomicity when storage replaces table rows, writes transaction WAL records, or replays the WAL at startup. Use when changing the covered catalog or storage paths.
---

# Verify WAL recovery atomicity

**Rule:** When restart opens a table after a row replacement, it must show all the old rows or all the new rows. If `Commit` returned success, it must show all the new rows.

**Harm:** A partial replacement can erase rows that were committed before the interrupted transaction.

## Coverage and sync

The model and probe were checked against `origin/main` at commit `62bb34395b5563ae8f5ab3d641c184ccd97bd274` and the working branch at `81883a79fb3c842310a739cdc49e2b3436b54c47`. Both revisions show the bug below:

| Code | What the check covers |
| --- | --- |
| `internal/catalog/publish.go`: `stageTableRows`, `replaceAllRows`, `appendRows`, `preparedRowSync.commit` | A changed table can become one storage transaction with a clear and inserts. |
| `internal/catalog/rows.go`: `storageRows.Begin`, `storageTxn.Commit`, `hydrateRowsFromEngine`, `hydrateTableRows` | The catalog uses storage commits and loads recovered rows. |
| `internal/storage/txn.go`: `Transaction.Clear`, `Transaction.Insert`, `Transaction.Commit`, `appendTransactionWAL`, `writeOverlay`, `publishCommittedOverlays` | The order of validation, log writes, sync, and publication. |
| `internal/storage/table.go`: `writeWALRecord` | Separate writes for the record length, checksum, and payload. |
| `internal/storage/wal.go`: `replayWALRecords`, `readWALRecord`, `applyWALPayload` | Rejection of incomplete records and replay of complete records. |
| `internal/storage/engine.go`: `Open` | Recovery finishes before the engine returns. |

Before each run, inspect changes to these functions since the sync commit, including uncommitted changes. Follow each changed action in the model and the real-code probe. The model does not read Go source. A passing model alone cannot show that it still matches the code. Move the sync commit forward only after the model and probe match the code at the new commit.

## Run

From the repository root:

```sh
.agents/skills/verify-wal-atomicity/run
```

The command runs TLC 1.7.4 and the real storage code. It needs Java 11 or later, the Go version in `go.mod`, Python 3, `shasum`, and `curl`. On first use it downloads the TLC jar to the user cache and checks its SHA-256. Set `TLA2TOOLS` to a jar with the same checksum to use it offline. The command uses no more than two processors for Go and TLC. It exits with an error if a check fails or cannot run.

Read every TLC result with `/statechart`, using `WALAtomicity.tla`, `WALAtomicity.cfg`, and `WALAtomicityView.js` from this skill. Read its counterexample or passing chart before reporting the result.

The real-code probe commits old rows `a,b`, commits a replacement with only `b`, and opens copies of the files at each byte prefix of the new log. A full commit must recover `b`. Each shorter prefix that opens must recover either `a,b` or `b`. An incomplete log can fail to open; the probe checks that the old and full logs open. Go reads the probe through an overlay. The command does not edit production files.

## Interpret and maintain

At the sync commit, both checks find a bug. TLC reports `AtomicRecovery`: the clear record is written, the process stops, and restart opens an empty table. The real-code probe reports an empty table after it reads 30 of the 65 bytes in the replacement log. A complete commit recovers `b`. Keep the check failing while this bug exists.

Compare each result with the covered code and the real-code probe. A model failure needs a real-code reproduction before you report a product bug. For a probe failure, trace the code and update the model if it left out that behavior. Report each new failure and its steps. Keep each rule when it fails. If a fix changes the log format or write order, update the model and probe. Show that the rule fails on broken code. Then run this command and `/statechart` again.

The model checks one table with old rows `a,b`, new row `b`, one writer, one crash, and restart. It models a crash between writes and assumes the written log prefix can survive before sync. The probe checks each byte prefix of this transaction. The checks leave out concurrent transactions, several tables, checkpoints, disk-write reordering, and other storage failures. They also leave out availability: a restart that refuses an incomplete log fails this separate concern.
