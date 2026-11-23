# Initialization

TLC reached 15 states and 25 steps. The chart draws 13 states and 20 arrows.
The chart does not draw 5 steps that stay in one chart state.

## CrashRetainsClaim breaks
The shortest path has 2 steps:
0. empty
1. Claim the empty directory: claimed
2. Process stops: crashed

## Rules that hold
NoDeadlock
