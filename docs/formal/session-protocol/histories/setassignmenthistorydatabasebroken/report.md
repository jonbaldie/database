# SetAssignmentHistory

TLC reached 5 states and 5 steps. The chart draws 5 states and 4 arrows.
The chart does not draw 1 steps that stay in one chart state.

## SelectedDatabasePersists breaks
The shortest path has 4 steps:
0. before USE
1. Select database: before SET
2. Apply first setting: first applied
3. Next setting is unknown: assignment failed
4. Return error before later assignments: complete

## Rules that hold
OnlySuccessfulPrefixApplied, NoDeadlock
