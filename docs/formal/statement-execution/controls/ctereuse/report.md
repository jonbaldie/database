# CTEReuse

TLC reached 3 states and 3 steps. The chart draws 3 states and 2 arrows.
The chart does not draw 1 steps that stay in one chart state.

## BuildOnce breaks
The shortest path has 2 steps:
0. CTE not read
1. Build CTE rows: First alias materialized
2. Build CTE rows: CTE built twice

## Rules that hold
NoDeadlock
