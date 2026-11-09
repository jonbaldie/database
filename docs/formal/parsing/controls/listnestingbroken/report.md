# ListNesting

TLC reached 8 states and 8 steps. The chart draws 8 states and 7 arrows.
The chart does not draw 1 steps that stay in one chart state.

## OnlyOuterCommaSplits breaks
The shortest path has 7 steps:
0. Scan position 1, depth 0, parts 1
1. Open: Scan position 2, depth 1, parts 1
2. Text: Scan position 3, depth 1, parts 1
3. Comma: Scan position 4, depth 1, parts 2
4. Text: Scan position 5, depth 1, parts 2
5. Close: Scan position 6, depth 0, parts 2
6. Comma: Scan position 7, depth 0, parts 3
7. Text: Scan position 8, depth 0, parts 3

## Rules that hold
NoDeadlock
