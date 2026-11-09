# LockCleanup

TLC reached 8 states and 10 steps. The chart draws 8 states and 9 arrows.
The chart does not draw 1 steps that stay in one chart state.

## FailedReadRestoresLocks breaks
The shortest path has 4 steps:
0. held
1. UpgradeWait: waiting for upgrade
2. GrantUpgrade: upgraded
3. LockNextRow: extra row locked
4. ReadFails: read failed

## Rules that hold
NoDeadlock
