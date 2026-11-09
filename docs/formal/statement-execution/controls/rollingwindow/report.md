# RollingWindow

TLC reached 5 states and 5 steps. The chart draws 3 states and 2 arrows.
The chart does not draw 3 steps that stay in one chart state.

## FrameCount breaks
The shortest path has 2 steps:
0. Window ready at row 1
1. Remove prior frame row: Old row removed
2. Add next frame row: Window contains an old row

## Rules that hold
NoDeadlock
