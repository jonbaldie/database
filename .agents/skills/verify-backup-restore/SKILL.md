---
name: verify-backup-restore
description: Check archive validation, staged restore, backup publication, or online backup chunk changes.
---

# Verify backup and restore

Rule: Restore installs only validated staged data. Reading archive entries requires a manifest and rejects duplicate files. A restored instance has a new identity and records its source identity.
Harm: Invalid data can become a usable instance, or archive corruption can be accepted as a good backup.

Run `.agents/skills/verify-backup-restore/run` from the repository root. It needs Python 3, Java, Go from go.mod, and the checksum-pinned TLC 1.7.4 jar from verify-wal-atomicity. It uses one TLC worker and at most two Go processors. A nonzero exit requires investigation.

Read each TLC result with `/statechart`. Use BackupRestore.tla and ArchiveValidation.tla with their matching configs and view modules. Follow the installed `/statechart` instructions to make the reports in a new scratch directory.

## Coverage and sync

Baseline: fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. Histories 4 and 36 belong to this subsystem.

| Source in cmd/database/backup_restore.go | Check |
| --- | --- |
| readBackupEntries, backupEntryReader.read/result/readManifest | Archive model tracks two file names, duplicate rejection, manifest presence, unsafe entry rejection, and end of input |
| validateBackupFileEntries | Real hash mismatch probe; metadata and hash validation are outside the entry-reader model |
| validateBackupCatalogReader and consumeBackupJSONValue | Real generated array depths 0 through 130; reject depth above 128 and trailing JSON |
| applyRestoreStaging, populateAndValidateRestore | Extract and Validate abstract completion of the synchronous staging and validation calls |
| installRestoreStaging | New destination rename versus two separate entry moves for an existing empty directory, then staging removal |
| restoreBackup, prepareRestoredMetadata | Real backup and restore probe reloads metadata and checks new/source identities |
| writeTemporaryBackupArchive | Real write-failure probe preserves prior output and removes the temporary file |

The restore model has two files, a new or an existing empty target, valid or invalid staged contents, and possible failure before or after each modeled install step. It preserves partial installation on failure for an existing target. It does not assert atomic restore into that target. Extract abstracts validation of the input archive and staging population; Validate abstracts instance and catalog validation before installation.

The archive model is an entry-reader model. An entries-read result is not a fully validated backup. Manifest schema, required file set, sizes, hashes, identities, and catalog shape are later validation steps. The real probe covers selected failures and a valid end-to-end backup and restore. Generated JSON input checks a bounded family of actual parser inputs, not every JSON document. No verifier of the Go parser itself is used.

## Check the checks and maintain

The broken restore model permits invalid staging to reach installation. The broken archive model accepts end of input without a manifest. Both must fail their rules. Separate real source overlays remove the manifest guard, hash guard, restore validation guard, or JSON depth limit. Each must fail at the expected source assertion. The runner uses temporary overlays and leaves product source unchanged.

Before changing a covered function, compare it with the sync commit, including working changes. Update the model and real-code probe together for intended changes. Replay model failures on real code before reporting a product bug. Advance the sync commit only after source review and successful checks. Use `/maintain-formal-verification-skill` after changes that did not run verification.

## Finite limits

The models do not cover arbitrary file counts, concurrent target changes, or filesystem power-loss behavior. The stream and publication extension below covers bounded chunk and publication histories. The source probes do not inject each partial-install failure or kill processes. Complete JSON grammar and all manifest fields remain outside these finite checks. run-controls adds dedicated missing-entry, whole-new-directory install, final-tail, and failed-publication controls. See docs/formal/coverage.md.

## Online chunks and publication

The same run command invokes run-stream. Read BackupChunks.tla, ArchivePublication.tla, and OnlineCapture.tla with `/statechart`, using their matching configs and view modules.

| Source | Model or real probe |
| --- | --- |
| internal/mysql/backup.go, backupChunkWriter.Write/flush | BufferByte, FlushFull, FlushTail; actual writes at sizes 0, 1, capacity minus one, capacity, capacity plus one, and twice capacity plus one |
| cmd/database/online_backup.go, writer/writeRow | ReadChunk; same-path rows reuse a handle and append bytes |
| finishOnlineBackupWriters | SyncFile, CloseFile, Advance and missing-file rejection; closed in the model represents a removed handle in the source |
| cmd/database/backup_restore.go, writeTemporaryBackupArchive | Write, Sync, Close, Rename; preserve old output before publication and on write failure |

BackupChunks models three input symbols and a buffer capacity of two. It abstracts equal-sized bytes and proves emitted plus buffered symbols equal the consumed prefix in that bound. The real probe uses the actual 64 KiB capacity, split writes, final flush, and yield failure. A separate exact-capacity probe detects duplicate emission after a missing buffer reset without running a broken writer that could loop forever.

OnlineCapture has two required files and at most three input chunks. It separates sync and close for each file. It does not model row scan errors, client connection failures, close errors, or deferred handle cleanup after those errors. The real probe checks append contents, allowed paths, required files, and that the handle map is empty after successful finish.

ArchivePublication abstracts successful write, sync, close, and rename, and a failure before publication. It assumes the rename result matches visibility. The real source overlay wraps rename and records a successful return from the actual file Sync call. The assertion verifies call order. It does not prove what a disk retains after a crash, or inject sync, close, or rename errors. The instrumentation leaves those file operations in place on the current-source probe.

The extension catches missing buffer reset, skipped sync, lost client handle reuse, and missing required-file rejection in separate source overlays. Three broken models fail their associated rules. The existing backup checks also run. Product source remains unchanged. Full end-to-end online protocol remains outside this model. Every configured rule has a broken model; source controls also check missing restored entries, partial new-directory exposure, failed archive replacement, and dropped final bytes.

The broken chunk-reset graph saturates after four emitted symbols to keep fault exploration finite. The current three-symbol input needs only one full-chunk flush, so this fault-only bound does not restrict its current transitions. The real negative probe uses an exact full chunk to avoid an endlessly flushing broken writer.

## Streamed JSON nesting

run-json checks JSONNesting with `/statechart` using the matching configs and View.js. It follows token position, a container stack and terminal acceptance across seven fixed inputs at depth limit two. Three controls permit excess depth, ignore container-end mismatch, or ignore trailing data. Real consumeBackupJSONValue probes check mixed containers, truncation, trailing data, and depths 127 through 130 against the real limit 128. Decoder tokenization, object key grammar, arbitrary JSON shapes, and all manifest fields remain assumptions or exclusions. See docs/formal/backup-restore/jsonnesting/results.md.
