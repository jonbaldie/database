# Backup stream and publication results

Baseline: fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1.
Command: `.agents/skills/verify-backup-restore/run`.
The combined command completed with exit 0 and ran both the original backup checks and the new stream checks. TLC 1.7.4 uses one worker. Go uses GOMAXPROCS=2 and GOFLAGS=-p=2.

| Check | Result |
| --- | --- |
| Chunk model | Pass; 6 states; two invariants and deadlock checked |
| Publication model | Pass; 8 states; two invariants and deadlock checked |
| Client capture model | Pass; 48 states; required closed files and deadlock checked |
| Three broken models | Fail prefix conservation, sync-before-publication, or required files respectively |
| Current source probes | Pass |
| Missing buffer reset | Fails duplicate full-chunk assertion |
| Skipped archive sync | Fails rename-before-sync assertion |
| Lost writer reuse | Fails chunk append assertion |
| Missing catalog accepted | Fails required catalog assertion |

Charts and /statechart reports are in backupchunks/, archivepublication/, and onlinecapture/. They hide only steps within a chart label: 3, 4, and 41 steps respectively, including byte collection, per-file sync/close in the coarse capture view, and terminal stuttering. Publication retains each distinct file-operation step. These charts do not draw transport connection state.

Bounds and scope are in the subsystem SKILL.md. Chunk capacity is two in the model and 64 KiB in the actual-source probe. The client model has two files and up to three chunks. Publication checks call order using an instrumented source overlay; it does not prove power-loss durability. Source probes do not inject every file-call failure or replay every process-stop boundary. The full online connection protocol and complete archive JSON grammar remain outside this extension.

All overlays and raw temporary logs were removed. Product source was unchanged.
