# AllocationLifecycle

TLC reached 31 states and 79 steps. The chart draws 5 states and 7 arrows.
The chart does not draw 44 steps that stay in one chart state.

## AllocationLifecycleSafe breaks
The shortest path has 1 step:
0. Uninitialized identifier state
1. Inspect column and scan existing values: Disabled table acquired identifier state

## Rules that hold
NoDeadlock
