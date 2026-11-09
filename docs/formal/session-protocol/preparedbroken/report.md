# Prepared

TLC reached 3 states and 3 steps. The chart draws 3 states and 2 arrows.
The chart does not draw 1 steps that stay in one chart state.

## ResetReleasesBytes breaks
The shortest path has 2 steps:
0. Prepared statement has no long data
1. Append a parameter chunk: Long data buffered
2. Reset retains parameter bytes: Reset retained long data

## Rules that hold
NoDeadlock
