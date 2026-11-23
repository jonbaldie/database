# EngineLifecycle

TLC reached 12 states and 15 steps. The chart draws 9 states and 10 arrows.
The chart does not draw 3 steps that stay in one chart state.

## FailedCommitUnfinished breaks
The shortest path has 2 steps:
0. Transaction active | unfinished | none
1. Stage a row: Private row staged | unfinished | none
2. Schema validation fails: Commit rejected | finished | validation error

## Rules that hold
SuccessFinishes, RepeatRejected, ClosedCommitRejected, ClosedBeginRejected, NoDeadlock
