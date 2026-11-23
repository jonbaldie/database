# OuterJoin

TLC reached 5 states and 5 steps. The chart draws 5 states and 4 arrows.
The chart does not draw 1 steps that stay in one chart state.

## PreserveUnmatchedLeft breaks
The shortest path has 4 steps:
0. Left row 1, match right rows
1. Return matching pair: Left row 1, finish left row
2. Advance to next left row: Left row 2, match right rows
3. No right row matches: Left row 2, finish left row
4. Advance to next left row: Unmatched left row lost

## Rules that hold
NoDeadlock
