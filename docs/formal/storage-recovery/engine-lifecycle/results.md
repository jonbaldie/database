# Engine and transaction lifecycle

Baseline is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1.

Run `.agents/skills/verify-storage-recovery/run-engine` from the repository root. It uses checksum-pinned TLC 1.7.4, one TLC worker, GOMAXPROCS=2, and GOFLAGS=-p=2. Current model and source probes pass. Five broken models and five source faults fail at the named rules and assertions. Restored current source passes again. Production files remain unchanged.

EngineLifecycle adds the lifecycle part of inventory history 6 to CommitRecovery. The current model has 12 states and 15 steps. The chart draws nine states and ten arrows. It hides three stuttering steps that keep the same chart state. Each configuration has its chart and report in the adjacent directory named after the configuration in lower case.

| Rule | Source owner | Broken source and assertion |
| --- | --- | --- |
| FailedCommitUnfinished | internal/storage/txn.go, Transaction.Commit validation return | Mark a transaction finished on failed validation. The probe fails with `failed validation finished transaction`. |
| SuccessFinishes | Transaction.Commit after publication and checkpoint attempt | Leave finished false after successful publication. The probe fails with `successful commit left transaction unfinished`. |
| RepeatRejected | Transaction.Commit finished guard | Bypass the finished guard. The probe fails with `second commit not rejected`. |
| ClosedCommitRejected | ensureCommitReady | Bypass the closed-engine guard. The probe fails with `closed engine accepted commit or finished transaction`. |
| ClosedBeginRejected | Engine.Begin | Bypass the closed-engine guard. The probe fails with `closed engine accepted begin`. |

The source probe opens a real engine, creates one table, stages and commits one row, and rejects a second commit. It starts another transaction, stages a row, reshapes the table, and checks that failed schema validation leaves the transaction unfinished. It starts an empty transaction, closes the engine, then checks commit and begin rejection and repeated close. Temporary Go overlays apply each fault separately. A build failure does not count as caught behavior.

The model starts with an open engine and one active transaction. It separates staging, successful commit, failed validation, repeated commit, closure before completion, commit after closure, and begin after closure. A validation failure leaves the transaction unfinished. The model then permits closure, rather than assuming that retry succeeds. Closed-engine commit uses an unfinished transaction, matching the real probe. Closure after an already finished transaction, arbitrary transaction counts, simultaneous calls, failed filesystem closure, and all WAL errors remain outside this small lifecycle model. CommitRecovery and delegated WALAtomicity retain the separate durability stages and replay checks.

Each current and broken configuration was checked through `/statechart` in a new external temporary directory. All rules were checked. No chart combines failing and passing states under one failure mark. Read EngineLifecycle.tla, its configuration, and EngineLifecycleView.js with the installed `/statechart` skill to produce a fresh output directory.
