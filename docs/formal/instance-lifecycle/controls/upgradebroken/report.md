# Upgrade

TLC reached 23 states and 35 steps. The chart draws 8 states and 10 arrows.
The chart does not draw 6 steps that stay in one chart state.

## ResumeKeepsTarget breaks
The shortest path has 3 steps:
0. stopped
1. Record upgrade target: marker written
2. Upgrade stops: interrupted
3. You request a different target: different target requested

## Rules that hold
CompletedTargetInstalled, IncompleteHasMarker, NoDeadlock
