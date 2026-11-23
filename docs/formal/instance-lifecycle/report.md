# InstanceLifecycle

TLC reached 20 states and 30 steps. The chart draws 10 states and 15 arrows.
The chart does not draw 5 steps that stay in one chart state.

## Rules that hold
ReadinessRequiresRuntime, DrainingIsNotReady, UncleanStopPreservesMarker, CleanStopHasStoppedMarker, RecoveryReflectsPriorMarker, NoDeadlock
