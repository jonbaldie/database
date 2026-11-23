# BackupRestore

TLC reached 28 states and 36 steps. The chart draws 7 states and 9 arrows.
The chart does not draw 12 steps that stay in one chart state.

## OnlyValidatedFilesInstalled breaks
The shortest path has 3 steps:
0. archive available
1. Extract into staging: staged
2. Staged validation succeeds: validated
3. Install the new directory: restored

## Rules that hold
RestoredHasBothEntries, NewDirectoryInstallIsWhole, NoDeadlock
