# SetMultiplicity

TLC reached 40 states and 40 steps. The chart draws 14 states and 20 arrows.
The chart does not draw 16 steps that stay in one chart state.

## IntersectionCount breaks
The shortest path has 2 steps:
0. Left rows read 0, duplicates remaining 1
1. Return one matched occurrence: Left rows read 1, duplicates remaining 1
2. Return one matched occurrence: Too many duplicate rows returned

## Rules that hold
NoDeadlock
