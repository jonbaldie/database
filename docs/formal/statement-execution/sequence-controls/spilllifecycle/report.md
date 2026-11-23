# SpillLifecycle

TLC reached 5 states and 5 steps. The chart draws 5 states and 4 arrows.
The chart does not draw 1 steps that stay in one chart state.

## DeliveredAndReleased breaks
The shortest path has 4 steps:
0. buffer rows
1. Spill: runs on disk
2. Merge: merge ready
3. Deliver: delivered
4. Close: Run files remain after close

## Rules that hold
NoDeadlock
