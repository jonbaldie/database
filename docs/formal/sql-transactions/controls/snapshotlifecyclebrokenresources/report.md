# SnapshotLifecycle

TLC reached 38 states and 56 steps. The chart draws 9 states and 9 arrows.
The chart does not draw 10 steps that stay in one chart state.

## ResourceFailureKeepsStage breaks
The shortest path has 6 steps:
0. idle
1. Begin: active
2. FirstRead: read
3. LaterPublish: updated outside
4. ReadAgain: read again
5. StageWrite: staged
6. RejectResources: resource rejected

## Rules that hold
NoDeadlock
