# WarningHistory

TLC reached 7 states and 7 steps. The chart draws 7 states and 6 arrows.
The chart does not draw 1 steps that stay in one chart state.

## ResetClearsWarnings breaks
The shortest path has 6 steps:
0. ordinary statement
1. Statement records warning: warning added
2. Read retained warnings: shown once
3. Read warnings again: shown twice
4. Begin ordinary statement; clear warnings: next statement
5. Statement fails; replace diagnostics: failure retained
6. Reset connection: reset

## Rules that hold
ShowRetainsWarnings, OrdinaryClearsWarnings, NoDeadlock
