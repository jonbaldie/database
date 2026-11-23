# BackupRestore

TLC reached 20 states and 24 steps. The chart draws 8 states and 9 arrows.
The chart does not draw 8 steps that stay in one chart state.

## NewDirectoryInstallIsWhole breaks
The shortest path has 3 steps:
0. archive available
1. Extract into staging: staged
2. Staged validation succeeds: validated
3. Install the new directory: install failed

## Rules that hold
NoDeadlock
