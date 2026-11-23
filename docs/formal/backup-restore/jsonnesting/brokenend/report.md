# JSONNesting

TLC reached 32 states and 32 steps. The chart draws 7 states and 10 arrows.
The chart does not draw 9 steps that stay in one chart state.

## AcceptedComplete breaks
The shortest path has 3 steps:
0. Read value with 0 open containers
1. Enter nested container: Read value with 1 open containers
2. Read scalar: Read value with 1 open containers
3. Accept unfinished input: Incomplete value accepted

## Rules that hold
DepthBound, NoDeadlock
