# EngineLifecycle

TLC reached 12 states and 15 steps. The chart draws 9 states and 10 arrows.
The chart does not draw 3 steps that stay in one chart state.

## ClosedCommitRejected breaks
The shortest path has 2 steps:
0. Transaction active | unfinished | none
1. Close the engine: Engine closed | unfinished | none
2. Try commit after close: Commit after engine close | finished | success

## Rules that hold
FailedCommitUnfinished, SuccessFinishes, RepeatRejected, ClosedBeginRejected, NoDeadlock
