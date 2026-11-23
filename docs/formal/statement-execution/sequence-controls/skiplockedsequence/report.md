# SkipLockedSequence

TLC reached 3 states and 3 steps. The chart draws 3 states and 2 arrows.
The chart does not draw 1 steps that stay in one chart state.

## AvailableOffsetOnly breaks
The shortest path has 2 steps:
0. skip offset, candidate 1
1. Inspect availability before counting offset: acquire result, candidate 2
2. Lock and return candidate: Returned row 2

## Rules that hold
NoDeadlock
