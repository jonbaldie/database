# BackupChunks

TLC reached 6 states and 6 steps. The chart draws 6 states and 5 arrows.
The chart does not draw 1 steps that stay in one chart state.

## CompletedStreamMatchesInput breaks
The shortest path has 5 steps:
0. writing | consumed 0 | buffer 0 | emitted 0
1. BufferByte: writing | consumed 1 | buffer 1 | emitted 0
2. BufferByte: writing | consumed 2 | buffer 2 | emitted 0
3. FlushFull: writing | consumed 2 | buffer 0 | emitted 2
4. BufferByte: writing | consumed 3 | buffer 1 | emitted 2
5. FlushTail: done | consumed 3 | buffer 0 | emitted 2

## Rules that hold
NoDeadlock
