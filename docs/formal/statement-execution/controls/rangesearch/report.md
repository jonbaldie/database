# RangeSearch

TLC reached 12 states and 12 steps. The chart draws 5 states and 5 arrows.
The chart does not draw 4 steps that stay in one chart state.

## InclusiveLowerBound breaks
The shortest path has 2 steps:
0. Search positions 0 through 3
1. Search at or below middle row: Search positions 0 through 1
2. Search above middle row: Lower boundary is wrong

## Rules that hold
NoDeadlock
