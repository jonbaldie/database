# ArchivePublication

TLC reached 7 states and 11 steps. The chart draws 6 states and 8 arrows.
The chart does not draw 3 steps that stay in one chart state.

## PublicationRequiresSync breaks
The shortest path has 4 steps:
0. temporary open
1. Write archive contents: written
2. Sync archive file: sync returned
3. Close archive file: closed
4. Install archive at output path: published

## Rules that hold
FailurePreservesOldOutput, NoDeadlock
