# EngineLifecycle

TLC reached 12 states and 15 steps. The chart draws 9 states and 10 arrows.
The chart does not draw 3 steps that stay in one chart state.

## RepeatRejected breaks
The shortest path has 3 steps:
0. Transaction active | unfinished | none
1. Stage a row: Private row staged | unfinished | none
2. Commit succeeds: Commit accepted | finished | success
3. Commit again: Second commit result | finished | success

## Rules that hold
FailedCommitUnfinished, SuccessFinishes, ClosedCommitRejected, ClosedBeginRejected, NoDeadlock
