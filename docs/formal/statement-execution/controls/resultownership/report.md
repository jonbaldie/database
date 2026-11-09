# ResultOwnership

TLC reached 5 states and 7 steps. The chart draws 5 states and 6 arrows.
The chart does not draw 1 steps that stay in one chart state.

## ReleaseOnce breaks
The shortest path has 2 steps:
0. result allocated
1. Release owned resources: result closed
2. Release owned resources: Result released twice

## Rules that hold
NoDeadlock
