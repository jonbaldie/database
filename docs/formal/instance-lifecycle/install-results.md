# Initialization and upgrade checks

Baseline: fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1.
Command: `.agents/skills/verify-instance-lifecycle/run`.
The combined runner completed with exit 0. It ran lifecycle, initialization, and upgrade checks. TLC is checksum-pinned to 1.7.4 and uses one worker. Go uses two runtime processors and two package jobs.

| Check | Result |
| --- | --- |
| Initialization model | Pass; 15 distinct states; four invariants and deadlock checked |
| Broken initialization model | Fails FailureRemovesOwnedCatalog |
| Current initialization source | Pass; success, second claim rejection, metadata conflict and owned-file cleanup |
| Disabled owned-catalog cleanup | Fails retained catalog assertion |
| Upgrade model | Pass; 23 distinct states; three invariants and deadlock checked |
| Broken resume model | Fails ResumeKeepsTarget |
| Current upgrade source | Pass; incomplete marker, resume validation, target installation, marker removal |
| Disabled target and source-ID validation | Fails resume mismatch assertion |
| Retained old version | Fails installed target assertion |

The /statechart runs checked these same configurations. Initialization draws 13 states and 20 arrows; upgrade draws eight states and ten arrows. Steps within one chart label are hidden: five initialization steps and six upgrade steps, including terminal stuttering and interrupted states with the same visible outcome. No distinct action labels were manually hidden. Charts and reports are in initialization/ and upgrade/ below this directory.

These models retain intermediate crash or interruption states. The probes exercise function seams and successful filesystem operations, plus a real metadata destination conflict. They do not crash a process at each modeled boundary or prove filesystem behavior after power loss. Initial staging-claim cleanup errors, upgrade preflight, matching backup, free-space checks, start/upgrade races, and dedicated mutations for every rule remain outside this check. Shutdown polling remains pending. See the subsystem skill for source mappings and maintenance steps.
