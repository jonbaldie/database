# ScalarSequence

TLC reached 11 states and 11 steps. The chart draws 8 states and 7 arrows.
The chart does not draw 3 steps that stay in one chart state.

## ScalarCardinality breaks
The shortest path has 3 steps:
0. open subquery, read 0 rows
1. Read: open subquery, read 1 rows
2. Read: finish, read 2 rows
3. Finish: Read 2 rows, returned value

## Rules that hold
NoDeadlock
