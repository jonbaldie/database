# MutationFinish

TLC reached 2 states and 2 steps. The chart draws 2 states and 1 arrows.
The chart does not draw 1 steps that stay in one chart state.

## RejectedAutocommitClearsStage breaks
The shortest path has 1 step:
0. mutation staged
1. Reject autocommit statement: Failed statement retained changes

## Rules that hold
NoDeadlock
