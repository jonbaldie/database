# PreparedHistory

TLC reached 7 states and 7 steps. The chart draws 7 states and 6 arrows.
The chart does not draw 1 steps that stay in one chart state.

## CachedTypesAvailable breaks
The shortest path has 3 steps:
0. absent
1. Prepare statement and reserve slot: allocated
2. Execute with supplied types: first execution
3. Execute with cached types: second execution

## Rules that hold
ConnectionResetReleasesPrepared, NoDeadlock
