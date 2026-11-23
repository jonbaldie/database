# SnapshotLifecycle

TLC reached 34 states and 44 steps. The chart draws 9 states and 9 arrows.
The chart does not draw 10 steps that stay in one chart state.

## RepeatableReadKeepsImage breaks
The shortest path has 4 steps:
0. idle
1. Begin: active
2. FirstRead: read
3. LaterPublish: updated outside
4. ReadAgain: read again

## Rules that hold
NoDeadlock
