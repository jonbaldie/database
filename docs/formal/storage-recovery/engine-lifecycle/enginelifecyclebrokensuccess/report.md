# EngineLifecycle

TLC reached 12 states and 15 steps. The chart draws 9 states and 10 arrows.
The chart does not draw 3 steps that stay in one chart state.

## SuccessFinishes breaks
The shortest path has 2 steps:
0. Transaction active | unfinished | none
1. Stage a row: Private row staged | unfinished | none
2. Commit succeeds: Commit accepted | unfinished | success

## Rules that hold
FailedCommitUnfinished, RepeatRejected, ClosedCommitRejected, ClosedBeginRejected, NoDeadlock
