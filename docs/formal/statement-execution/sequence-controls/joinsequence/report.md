# JoinSequence

TLC reached 7 states and 7 steps. The chart draws 7 states and 6 arrows.
The chart does not draw 1 steps that stay in one chart state.

## MatchThenPreserveLeft breaks
The shortest path has 6 steps:
0. build right, left row 1
1. Build: probe, left row 1
2. Probe: emit, left row 1
3. Emit: next left, left row 1
4. Probe next left row: probe, left row 2
5. Probe: emit, left row 2
6. Emit: Unmatched left row lost

## Rules that hold
NoDeadlock
