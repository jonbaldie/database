# AllocationLifecycle

TLC reached 55 states and 143 steps. The chart draws 6 states and 10 arrows.
The chart does not draw 82 steps that stay in one chart state.

## AllocationLifecycleSafe breaks
The shortest path has 2 steps:
0. Uninitialized identifier state
1. Inspect column and scan existing values: Next identifier exceeds stored values
2. Truncate table and reset allocator: Truncation kept prior identifier state

## Rules that hold
NoDeadlock
