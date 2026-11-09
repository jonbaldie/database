# AllocationLifecycle

TLC reached 40 states and 120 steps. The chart draws 6 states and 10 arrows.
The chart does not draw 59 steps that stay in one chart state.

## AllocationLifecycleSafe breaks
The shortest path has 1 step:
0. Uninitialized identifier state
1. Inspect column and scan existing values: Next identifier repeats an existing value

## Rules that hold
NoDeadlock
