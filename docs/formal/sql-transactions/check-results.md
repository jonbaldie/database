# SQL transaction check results

Baseline: fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1.
Command: `.agents/skills/verify-sql-transactions/run` from the repository root.
Tool: checksum-pinned TLC 1.7.4; one worker. Go uses GOMAXPROCS=2 and GOFLAGS=-p=2.

| Check | Result |
| --- | --- |
| SQLTransactions model | Pass; 34 distinct states, all five configured rules checked including deadlock |
| Broken prefix model | Fails SavepointRestoresPrefix |
| Current source probe | Pass |
| Removed prefix restoration | Fails savepoint prefix assertion |
| Disabled transaction cleanup | Fails retained state assertion |
| Disabled one-shot consumption | Fails settings assertion |
| Delegated lock model | Pass; 39 distinct states |
| Delegated lock source probes | Pass |
| Disabled wait-cycle detection | Fails expected deadlock assertion |

The runner completed with exit 0. Product source was unchanged. The result confirms the stated bounds, not the whole transaction subsystem.

The /statechart run checked the same current model and drew five chart states and seven arrows. It hides 53 model steps whose labels do not change, including reads, writes, and rollback within the same savepoint state. The diagram shows control flow; it does not show row data or durable recovery. All chart states are reachable and terminal states have stuttering steps in the model.

The combined command now also checks lazy snapshot capture, isolation refresh, resource rejection, publication rejection, wait cleanup, and savepoint lock retention. General duplicate-name stacks remain outside the stated finite model. See the skill for source controls and bounds.

## Integration update

The latest combined runner includes all extensions described in the skill. See [integration results](../integration-results.json) for its outcome. Earlier state counts above refer to the initial models. The coverage map records the final sequence scope. Product source remains unchanged.
