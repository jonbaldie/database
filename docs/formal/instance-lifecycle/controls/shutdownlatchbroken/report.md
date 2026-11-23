# ShutdownLatch

TLC reached 7 states and 10 steps. The chart draws 3 states and 4 arrows.
The chart does not draw 6 steps that stay in one chart state.

## FirstRequestStaysLatched breaks
The shortest path has 2 steps:
0. Server accepts requests
1. Request shutdown: Shutdown requested by a
2. Request shutdown: Shutdown requested by b

## Rules that hold
NoDeadlock
