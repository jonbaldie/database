# SetFoldSequence

TLC reached 3 states and 3 steps. The chart draws 3 states and 2 arrows.
The chart does not draw 1 steps that stay in one chart state.

## IntersectBeforeUnion breaks
The shortest path has 2 steps:
0. terms evaluated
1. Intersect: fold union
2. Union: Wrong values published

## Rules that hold
NoDeadlock
