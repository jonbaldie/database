# SpillMerge

TLC reached 4 states and 4 steps. The chart draws 4 states and 3 arrows.
The chart does not draw 1 steps that stay in one chart state.

## SortedRows breaks
The shortest path has 3 steps:
0. Rows merged 0
1. Return next row from second run: Rows merged 1
2. Return next row from first run: Rows merged 2
3. Return next row from first run: Rows returned out of order

## Rules that hold
NoDeadlock
