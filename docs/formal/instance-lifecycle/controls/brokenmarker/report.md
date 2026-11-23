# InstanceLifecycle

TLC reached 20 states and 30 steps. The chart draws 10 states and 16 arrows.
The chart does not draw 4 steps that stay in one chart state.

## UncleanStopPreservesMarker breaks
The shortest path has 3 steps:
0. Server stopped
1. You start the server: Starting server
2. Record the running state: Starting server
3. Startup fails or the process stops: Stopped without a clean record

## Rules that hold
ReadinessRequiresRuntime, DrainingIsNotReady, CleanStopHasStoppedMarker, RecoveryReflectsPriorMarker, NoDeadlock
