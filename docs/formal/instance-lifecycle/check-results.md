# Instance lifecycle check results

Baseline: fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1.
Command: `.agents/skills/verify-instance-lifecycle/run` from the repository root.
Tool: checksum-pinned TLC 1.7.4, one worker; Go uses GOMAXPROCS=2 and GOFLAGS=-p=2.

| Check | Result |
| --- | --- |
| Current model | Pass; 20 distinct states; five explicit invariants and deadlock checked |
| Broken unclean marker model | Fails UncleanStopPreservesMarker |
| Broken readiness model | Fails DrainingIsNotReady |
| Current source probes | Pass |
| Clearing marker after unclean finish | Fails clean-flag marker assertion |
| Keeping readiness during shutdown | Fails endpoint assertion: got 200, expected 503 |
| Shared ownership lock | Fails second-owner rejection |

The runner completed with exit 0. Product source was unchanged. Temporary source overlays were removed after the run.

The /statechart result covers the current configuration. It draws ten chart states and fifteen arrows. Five model steps stay within one chart label and are not drawn; these include terminal stuttering and the clean-start running-marker transition. A recovered startup has its own visible state. The chart keeps every model state reachable through its labels and retains exit arrows from nonterminal labels.

Bounds: one owner, two possible prior marker values, one start and one stop. The source probe checks real advisory locks and file markers. It calls readiness and stop seams directly. It does not start a runtime, kill a process, inject disk faults, prove power-loss durability, or verify actual drain behavior. A pass covers these stated checks. Initialization, upgrade, shutdown polling, and request latching are also checked by the combined command.

## Integration update

The latest combined runner includes all extensions described in the skill. See [integration results](../integration-results.json) for its outcome. Earlier state counts above refer to the initial models. The coverage map records the final sequence scope. Product source remains unchanged.
