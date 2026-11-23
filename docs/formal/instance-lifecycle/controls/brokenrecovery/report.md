# InstanceLifecycle

TLC reached 20 states and 30 steps. The chart draws 9 states and 13 arrows.
The chart does not draw 5 steps that stay in one chart state.

## RecoveryReflectsPriorMarker breaks
The shortest path has 2 steps:
0. Server stopped
1. You start the server: Starting server
2. Record the running state: Starting server

## Rules that hold
NoDeadlock
