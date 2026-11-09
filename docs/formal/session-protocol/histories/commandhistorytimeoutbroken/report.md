# CommandHistory

TLC reached 7 states and 9 steps. The chart draws 7 states and 6 arrows.
The chart does not draw 3 steps that stay in one chart state.

## IdleExpiryRollsBack breaks
The shortest path has 3 steps:
0. executing
1. Read next command during active statement: buffered
2. Active statement returns: idle
3. Idle transaction deadline expires: timeout

## Rules that hold
ReadAheadPreservesCommand, RevocationStopsDispatch, NoDeadlock
