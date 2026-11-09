# Formal verification coverage

Source baseline is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1.

Eight subsystem skills now assign a bounded model and real-code check to every one of the 36 inventory histories. The models include the original sequence stages at finite bounds. They do not prove all inputs, concurrent schedules, or failure combinations. The skills record exact source functions, finite bounds, exclusions, and maintenance steps. No production source file changed.

The integration runner checks all eight subsystem commands for their stated outcomes. The packet command reproduces a current continuation defect and returns 1 deliberately. Its repair exists only in a temporary overlay. Current result-stream checks pass. See [packet finding](packet-continuation/check-results.md) and [stream results](packet-continuation/stream-results.md).

| Subsystem skill | History IDs | Checked sequences and rules | Evidence |
| --- | --- | --- | --- |
| [verify-instance-lifecycle](../../.agents/skills/verify-instance-lifecycle/SKILL.md) | 1, 2, 3, 5 | Initialization, ownership/readiness, upgrade/resume, shutdown polling | [Results](instance-lifecycle/check-results.md) |
| [verify-backup-restore](../../.agents/skills/verify-backup-restore/SKILL.md) | 4, 36 | Staged restore, archive entries, chunks, capture, publication | [Results](backup-restore/check-results.md) |
| [verify-storage-recovery](../../.agents/skills/verify-storage-recovery/SKILL.md) | 6–13 | Commit/recovery, WAL, checkpoint, schema, hydration, revision claim, cache, allocation | [Results](storage-recovery/details-results.md) |
| [verify-session-protocol](../../.agents/skills/verify-session-protocol/SKILL.md) | 14, 15, 16, 19, 20, 21, 23, 24 | Admission, capabilities, cancellation, prepared reset, settings, grants, diagnostics, explanation | [Results](session-protocol/check-results.md) |
| [verify-sql-transactions](../../.agents/skills/verify-sql-transactions/SKILL.md) | 17, 18 | Transaction cleanup, savepoints, one-shot settings, delegated row locks | [Results](sql-transactions/check-results.md) |
| [verify-statement-execution](../../.agents/skills/verify-statement-execution/SKILL.md) | 22, 26–34 | Resources, result close, sort, limit, CTE, window, join, set counts, mutation finish, range | [Results](statement-execution/check-results.md) |
| [verify-packet-protocol](../../.agents/skills/verify-packet-protocol/SKILL.md) | 25 | Frame sequence and result-stream termination | [Results](packet-continuation/check-results.md) |
| [verify-parsing](../../.agents/skills/verify-parsing/SKILL.md) | 35 | Quoted literals, depth guards, command flags | [Results](parsing/check-results.md) |

## Each inventory history

| ID | Model or delegated check | Limit that matters |
| --- | --- | --- |
| 1 | Initialization | One staged install and one metadata conflict; no general filesystem failure proof. |
| 2 | InstanceLifecycle | One owner and start/stop; failed real startup, marker/lock probes; drain delegates to AdmissionDrain. |
| 3 | Upgrade | One old/target version pair; resume identity checks, no backup preflight. |
| 4 | BackupRestore, BackupChunks, BackupCapture, BackupPublication | Bounded files/chunks; no arbitrary connection failures. |
| 5 | InstanceLifecycle, ShutdownPolling, ShutdownLatch | Two latched requests, six poll attempts, and fake-clock source loop; no live drain race. |
| 6 | CommitRecovery, EngineLifecycle | One transaction, staged/rejected/successful/repeated commit, closed-engine commit/begin rejection; no concurrent Close/Commit. |
| 7 | Delegated WALAtomicity | Two tables and byte prefixes of one replacement; no arbitrary WAL histories. |
| 8 | CheckpointPublication | One checkpoint and row image; hook returns, no power loss. |
| 9 | TableSchema, SchemaVersion | One table create/reshape/drop/recreate, metadata stages and selected errors; no power loss. |
| 10 | CatalogHydration | Legacy/durable image selection. |
| 11 | CatalogPublication, CatalogRevision | One writer batch through sync, schema persistence, revision and notification; selected failure paths. |
| 12 | OrderedCache | One cached image and one change. |
| 13 | AllocationLifecycle, AutoIncrement | Disabled/scan/generated/explicit/exhausted/truncate; values 0–3, source limit through 255. |
| 14 | AdmissionDrain, Admission, ConnectExecute | One pending/session slot plus shutdown rejection and drain; no arbitrary concurrent admission. |
| 15 | AuthenticationHistory, Handshake | Greeting, TLS, token rejection, RSA request, full-password exchange, success/failure; crypto internals abstracted. |
| 16 | CommandHistory, Cancellation | Idle/execute/read-ahead/revocation/close and idle rollback; no arbitrary transport race. |
| 17 | SQLTransactions, SnapshotLifecycle, SavepointLocks | One transaction, lazy image and two isolation modes, resource/publication failure, savepoint prefix and lock retention; no general SQL isolation proof. |
| 18 | LockCleanup, delegated RowLockManager | Bounded wait graph, upgrade, cancellation, timeout, snapshot restore and real failed locking SELECT; no arbitrary concurrent history. |
| 19 | PreparedHistory, Prepared | Allocate, cached types, execute, long data, reset and connection reset; no all-type parameter decode. |
| 20 | SetAssignmentHistory, Settings | Ordered SET success prefix, selected database and byte ceiling; no arbitrary settings. |
| 21 | AccountHistory, AccountGrant | Create/change/lock/unlock/drop and existing-session revocation; no last-manager policy proof. |
| 22 | Resources | Bounded reservations, failure/finalize/close and cancellation/deadline effects; no concurrent release. |
| 23 | WarningHistory, Diagnostics | Statement clear, accumulation, SHOW retention and reset; bounded warning count. |
| 24 | LiveExplanation, ConnectExecute | Two entries for one connection; no concurrent metrics. |
| 25 | PacketProtocol, ResultStream | Two-frame continuation and bounded stream; current sequence defect reproduced. |
| 26 | ResultOwnership | Sequential release once; no concurrent close. |
| 27 | SpillMerge | Buffer/spill/merge/delivery/cleanup with two runs; real three-row forced spill. |
| 28 | LimitDelivery | Bounded rows/offset/count and SKIP LOCKED candidate accounting; no general planner proof. |
| 29 | CTEReuse | CTE reuse, scalar cardinality and correlated versus uncorrelated evaluation; no recursive CTE semantics. |
| 30 | RollingWindow | Partition, peers/rank and moving frame on fixed rows; no all-frame aggregate proof. |
| 31 | OuterJoin | Build/probe/matched/unmatched on fixed keys; no general join semantics. |
| 32 | SetMultiplicity | Bounded multiplicities, three-operand precedence and NULL-aware membership; no arbitrary set query. |
| 33 | MutationFinish | Ordered UPDATE/REPLACE private candidates and rejected ALTER action before publication; no arbitrary DML/foreign-key proof. |
| 34 | RangeSearch | Lower/upper construction and tightening with inclusivity; no general expression optimizer proof. |
| 35 | QuotedSQL, CommentModes, ListNesting, DepthGuard, CommandFlags | Selected scanner/depth/flag rules; no complete SQL grammar. |
| 36 | ArchiveValidation, JSONNesting, backup validation probes | Bounded manifest/seen paths, token cursor/container stack and real depth cases; no complete archive/JSON grammar. |

## Checked user flows

These flows were selected from source based on data durability and common operator/client tasks. No usage telemetry was available.

- [Start and stop](instance-lifecycle/chart.mmd) separates ownership, readiness, shutdown, and marker handling. The session AdmissionDrain model checks a bounded admitted statement; full runtime teardown remains an assumption.
- [Connect and execute](session-protocol/connectexecute/chart.mmd) checks one supported statement. The [AuthenticationHistory charts](session-protocol/histories/authenticationhistory/chart.mmd) include the supported TLS and RSA paths; cryptographic primitives remain assumptions.
- [Commit and restart](storage-recovery/chart.mmd) checks one transaction and a process stop. It does not model power loss.
- [Restore](backup-restore/backuprestore/chart.mmd) checks bounded staged installation. [Backup publication](backup-restore/archivepublication/chart.mmd) checks sync before rename.

Each chart has an adjacent report.md with reached states, drawn labels, arrows, and omitted same-label steps. A drawing merges model states with equal labels. It is not the full state graph.

## Evidence and remaining gaps

Every selected boundary has a current source probe and a demonstrated broken-source failure. Broken models and source overlays must fail the named invariant or behavioral assertion. A compiler failure is not negative evidence. Current models pass except for the retained packet finding; its repaired model and source overlay pass.

General concurrent schedules, arbitrary transport failures, full SQL semantics, arbitrary DML publication, power-loss ordering, and exhaustive I/O faults remain unchecked. These limits must accompany any verification claim.

Run each skill command from the repository root. [Integration results](integration-results.json) record all eight expected outcomes. Full command logs are retained in the local workspace audit trail. Go uses GOMAXPROCS=2 and GOFLAGS=-p=2. TLC 1.7.4 is checksum pinned. The packet check intentionally remains red until its reported defect is fixed.

## Scope audit

The original inventory is retained in [history-inventory.md](history-inventory.md). An independent review found that early checks covered guards instead of full sequences. The following extensions close those named gaps at finite bounds.

| Histories | Sequence evidence |
| --- | --- |
| 5 | ShutdownLatch and ShutdownPolling in the instance skill |
| 6 | EngineLifecycle and its five model/source controls in the storage skill |
| 9, 11, 13 | [Storage stages and failures](storage-recovery/sequence-results.md) |
| 14–16, 19–21, 23 | [Session histories](session-protocol/history-results.md) |
| 17, 18 | SnapshotLifecycle, LockCleanup and SavepointLocks in the SQL skill; real failed SELECT in statement-execution |
| 22, 27–34 | [Execution histories](statement-execution/check-results.md) |
| 35 | CommentModes and ListNesting in the parsing skill |
| 36 | [Streamed JSON nesting](backup-restore/jsonnesting/results.md) |

Supporting lifecycle and backup invariants now have dedicated negative controls. The complete current/broken model graphs must remain finite. The broken chunk reset model saturates after four emitted symbols; this bound only limits the faulty loop. No production source was edited.

## Reproduce the flow charts

The diagrams use these current models and their same-name .cfg and View.js files.

| User flow | Skill directory | Model |
| --- | --- | --- |
| Start and stop | verify-instance-lifecycle | InstanceLifecycle |
| Authenticate | verify-session-protocol | AuthenticationHistory |
| Commit and restart | verify-storage-recovery | CommitRecovery |
| Restore | verify-backup-restore | BackupRestore |
| Publish a backup | verify-backup-restore | ArchivePublication |

Run the chart command in fresh scratch space. This example reproduces start and stop. Each adjacent report states its reached states and hidden same-label steps. Substitute the directory and model above for the other flows.

```sh
repo=$(pwd -P)
STATECHART=/path/to/installed/statechart/scripts/statechart
chart_scratch=$(mktemp -d)
cp "$repo/.agents/skills/verify-instance-lifecycle/InstanceLifecycle.tla" "$chart_scratch/"
cp "$repo/.agents/skills/verify-instance-lifecycle/InstanceLifecycle.cfg" "$chart_scratch/"
cp "$repo/.agents/skills/verify-instance-lifecycle/InstanceLifecycleView.js" "$chart_scratch/"
cd "$chart_scratch"
"$STATECHART" InstanceLifecycle.tla --config InstanceLifecycle.cfg --view InstanceLifecycleView.js --out fresh-chart
```
