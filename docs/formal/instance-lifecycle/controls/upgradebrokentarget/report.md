# Upgrade

TLC reached 16 states and 25 steps. The chart draws 8 states and 10 arrows.
The chart does not draw 4 steps that stay in one chart state.

## CompletedTargetInstalled breaks
The shortest path has 4 steps:
0. stopped
1. Record upgrade target: marker written
2. Record incomplete upgrade: incomplete
3. Install target version: target written
4. Remove upgrade marker: complete

## Rules that hold
NoDeadlock
