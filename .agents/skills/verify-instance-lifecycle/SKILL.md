---
name: verify-instance-lifecycle
description: Check data-directory ownership, running markers, readiness, shutdown, initialization, or upgrade transitions in the instance subsystem.
---

# Verify the instance lifecycle

Rule: A live owner excludes another owner of the same directory. Readiness is removed when shutdown begins. A clean stop records stopped; an unclean stop retains running for recovery.
Harm: Two servers can change one directory, clients can receive work during shutdown, or the next start can miss an unclean stop.

Run `.agents/skills/verify-instance-lifecycle/run` from the repository root. It needs Python 3, Java, Go from go.mod, and the checksum-pinned TLC 1.7.4 jar installed by verify-wal-atomicity. It uses one TLC worker and at most two Go processors. A nonzero exit needs investigation.

Read each model result with `/statechart`. Use InstanceLifecycle.tla, InstanceLifecycle.cfg, and InstanceLifecycleView.js here. Follow the installed skill instructions to produce its outputs in scratch space.

## Coverage and sync

Baseline: fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. Histories 1, 2, 3, and 5 belong to this subsystem. This check covers initialization installation and cleanup, process ownership and clean-stop parts of history 2, upgrade marker and resume rules, and shutdown readiness. Shutdown polling has a bounded model and a deterministic source probe.

| Source in internal/lifecycle/server.go | Check |
| --- | --- |
| claimDataDirectory, claimState | ClaimDirectory and MarkRunning; real second-owner rejection |
| markStateRunning, stateWasRunning | Prior marker selection and recovered flag; real restart after each finish mode |
| serve, startRuntime, reportReady | Runtime start precedes readiness in the model; reportReady event and endpoint checked directly |
| awaitStop, diagnosticsHandler | RequestStop; cancelled context removes readiness and emits stopping |
| runtime.closeGracefully | DrainRuntime abstraction; actual draining is outside this probe |
| claimedState.finish, releaseState | Separate marker write and ownership release; real clean and unclean finish |

The finite model has one owner, two possible prior markers, one start, and one stop. It splits lock acquisition, marker write, runtime start, readiness, shutdown request, drain, clean marker, and release. It abstracts successful file calls. Failure or process stop after the running marker retains it. Crashes during a marker write, operating-system power loss, and disk errors are outside the model. In particular finish ignores marker-write errors; the clean-marker rule assumes that write succeeds.

The model records a failed startup or process stop with the same unclean terminal label. The real probe uses finish(false), not a process crash. It tests actual advisory locks and writes and reloads the state file. The probe calls reportReady directly with an empty runtime to check the endpoint and event. A failed real serve call must not emit readiness. Successful full server boot remains outside the probe.

## Prove and maintain

The lifecycle runner checks five broken models and six separate source overlays. Clearing the marker after an unclean stop must fail the marker assertion. Keeping ready during shutdown must fail the endpoint assertion. Replacing exclusive locks with shared locks must fail second-owner rejection. Production source remains unchanged.

Compare covered functions with the sync commit, including working changes. Update model and probe together when behavior changes. Replay model failures on real code before reporting a product bug. Advance the sync commit only after source review and both checks pass. Use `/maintain-formal-verification-skill` after changes that did not run this check.

Runtime readiness, clean-stop markers, and recovery classification each have a broken model and source control. Failed startup must not announce readiness. See docs/formal/coverage.md for the finite coverage map.

## Initialization and upgrade

The same run command also runs run-install. Read Initialization.tla and Upgrade.tla with `/statechart`, using their matching configs and view files.

| Source | Model or probe |
| --- | --- |
| internal/instance/instance.go, claimInitialization | Exclusive staging directory; real second claim rejection |
| initializationPaths.writeStaged, commit, installStaged | Stage both files, link and unlink catalog, then link and unlink metadata |
| removeInstalled, discard, release | Failed metadata installation removes the owned catalog; successful installation removes the claim |
| cmd/database/upgrade.go, prepareUpgrade | Write marker before incomplete metadata; preflight and upgrade lock are outside this model |
| markUpgradeIncomplete, completeUpgrade | Incomplete metadata, target metadata, then marker removal |
| validateUpgradeState | Same target and source instance on resume; real probes reject both mismatch types |

Initialization has one empty target, one attempt, successful file calls or one metadata destination conflict, and a possible process stop between modeled operations. `metadata` in its model means metadata installed by this attempt; a conflicting pre-existing file is external. The real probe verifies that the conflicting file stays unchanged. Cleanup is split into owned-catalog removal and claim discard; errors during cleanup, write/link failure classes beyond the conflict, and directory-sync power-loss guarantees are outside the check.

Upgrade has one instance and one old/target version pair. Interruption is allowed after marker write, incomplete metadata, or target installation. Repeated same-target resumes remain finite because the model keeps only version and metadata state. The real probe writes a marker, marks the initialized instance incomplete, rejects wrong target and source IDs, completes, and reloads target metadata. It does not replay process stops or run backup preflight. Disk errors, concurrent start/upgrade races, free-space checks, unsupported-version policy, and archive matching remain outside these models.

Broken initialization cleanup must fail its source assertion and model rule. Broken upgrade resume must fail its source assertion and model rule. A source overlay that retains the old version must fail the target assertion. Separate controls check success-file completeness, interrupted claim retention, metadata ordering, incomplete-upgrade marker retention, and target installation. run-install checks seven broken models and seven source controls. These are bounded checks for histories 1 and 3; they are not full filesystem crash proofs.

## Shutdown polling

The same command runs run-polling. Read ShutdownPolling.tla, its matching current and broken configs, and ShutdownPollingView.js with `/statechart`. The model checks at most six connection attempts. A successful connection resets the failure count. Stop confirmation requires three consecutive failures. The current graph has 25 states.

The source probe calls cmd/database/shutdown.go, waitForOnlineShutdown. A temporary overlay supplies a fake clock and connection outcomes. Two failures, one success, then three failures must take six attempts. Repeated successes must reach the 30-second deadline. Broken reset and threshold models and source overlays must fail. This tests the real polling loop with controlled dependencies. It does not test a live server drain, network timing, or whether a connection failure proves that a remote process exited.

## Shutdown request latch

run-latch checks ShutdownLatch with at most two requests and two operation identifiers. RequestShutdown must close its signal once and retain the first identifier. A broken model and source overlay replace the first identifier with the second. This checks the real latch, separate from polling and admitted-session draining.
