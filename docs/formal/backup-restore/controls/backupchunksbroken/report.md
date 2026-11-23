# BackupChunks

TLC reached 5 states and 5 steps. The chart draws 5 states and 4 arrows.
The chart does not draw 1 steps that stay in one chart state.

## BufferedAndEmittedMatchInput breaks
The shortest path has 3 steps:
0. writing | consumed 0 | buffer 0 | emitted 0
1. BufferByte: writing | consumed 1 | buffer 1 | emitted 0
2. BufferByte: writing | consumed 2 | buffer 2 | emitted 0
3. FlushFull: writing | consumed 2 | buffer 2 | emitted 2

## Rules that hold
CompletedStreamMatchesInput, NoDeadlock
