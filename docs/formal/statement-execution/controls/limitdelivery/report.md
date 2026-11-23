# LimitDelivery

TLC reached 51 states and 51 steps. The chart draws 6 states and 7 arrows.
The chart does not draw 16 steps that stay in one chart state.

## ExactSlice breaks
The shortest path has 2 steps:
0. Rows scanned 0, rows emitted 0
1. Skip offset row: Rows scanned 1, rows emitted 0
2. Emit next requested row: Wrong row slice returned

## Rules that hold
NoDeadlock
