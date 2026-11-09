# Diagnostics

TLC reached 3 states and 3 steps. The chart draws 3 states and 2 arrows.
The chart does not draw 1 steps that stay in one chart state.

## OwnsDiagnostics breaks
The shortest path has 2 steps:
0. Caller owns a warning
1. Replace session diagnostics: Session stores the warning
2. Change the caller diagnostic: Caller change altered session diagnostic

## Rules that hold
NoDeadlock
