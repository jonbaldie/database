# LockCleanup

TLC reached 8 states and 10 steps. The chart draws 8 states and 9 arrows.
The chart does not draw 1 steps that stay in one chart state.

## WaitExitClearsEdge breaks
The shortest path has 2 steps:
0. held
1. UpgradeWait: waiting for upgrade
2. WaitExit: cancelled

## Rules that hold
NoDeadlock
