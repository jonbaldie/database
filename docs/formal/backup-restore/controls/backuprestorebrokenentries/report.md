# BackupRestore

TLC reached 19 states and 23 steps. The chart draws 8 states and 10 arrows.
The chart does not draw 7 steps that stay in one chart state.

## RestoredHasBothEntries breaks
The shortest path has 5 steps:
0. archive available
1. Extract into staging: staged
2. Staged validation succeeds: validated
3. Move first entry into existing empty directory: partly installed
4. Move second entry: entries installed
5. Remove staging directory: restored

## Rules that hold
NoDeadlock
