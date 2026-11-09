# WindowSequence

TLC reached 7 states and 7 steps. The chart draws 7 states and 6 arrows.
The chart does not draw 1 steps that stay in one chart state.

## PeerRankAndFrame breaks
The shortest path has 6 steps:
0. partition sorted, row 1
1. Advance: frame ready, row 1
2. Emit: partition sorted, row 2
3. Advance: frame ready, row 2
4. Emit: partition sorted, row 3
5. Advance: frame ready, row 3
6. Emit: Wrong rank after peers

## Rules that hold
NoDeadlock
