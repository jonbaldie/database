# JSONNesting

TLC reached 32 states and 32 steps. The chart draws 7 states and 9 arrows.
The chart does not draw 10 steps that stay in one chart state.

## AcceptedComplete breaks
The shortest path has 2 steps:
0. Read value with 0 open containers
1. Read scalar: value complete
2. Accept trailing value: Trailing value accepted

## Rules that hold
DepthBound, NoDeadlock
