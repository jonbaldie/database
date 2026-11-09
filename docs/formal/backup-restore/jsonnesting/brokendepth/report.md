# JSONNesting

TLC reached 37 states and 37 steps. The chart draws 7 states and 10 arrows.
The chart does not draw 11 steps that stay in one chart state.

## DepthBound breaks
The shortest path has 3 steps:
0. Read value with 0 open containers
1. Enter nested container: Read value with 1 open containers
2. Enter nested container: Read value with 2 open containers
3. Enter nested container: Read value with 3 open containers

## Rules that hold
AcceptedComplete, NoDeadlock
