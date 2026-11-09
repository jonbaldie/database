# SQLTransactions

TLC reached 52 states and 236 steps. The chart draws 5 states and 8 arrows.
The chart does not draw 89 steps that stay in one chart state.

## ReadOnlyHasNoMutations breaks
The shortest path has 2 steps:
0. Ready to begin
1. You begin a transaction: Transaction active
2. You change rows: Transaction active

## Rules that hold
NoDeadlock
