# Cancellation

TLC reached 3 states and 3 steps. The chart draws 3 states and 2 arrows.
The chart does not draw 1 steps that stay in one chart state.

## CloseOnce breaks
The shortest path has 2 steps:
0. Cancellation watch open
1. Request cancellation: Cancellation delivered
2. Close the cancelled watch again: Same watch closed twice

## Rules that hold
NoDeadlock
