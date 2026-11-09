# SavepointLocks

TLC reached 5 states and 5 steps. The chart draws 5 states and 4 arrows.
The chart does not draw 1 steps that stay in one chart state.

## RollbackRetainsLocks breaks
The shortest path has 3 steps:
0. active
1. Save: saved
2. WriteAndLock: changed
3. RollbackTo: restored

## Rules that hold
NoDeadlock
