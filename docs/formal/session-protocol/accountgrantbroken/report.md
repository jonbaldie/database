# AccountGrant

TLC reached 4 states and 4 steps. The chart draws 4 states and 3 arrows.
The chart does not draw 1 steps that stay in one chart state.

## RevokeRemovesGrant breaks
The shortest path has 3 steps:
0. No read grant
1. Grant read access: Read grant active
2. Grant the same access again: Repeated grant unchanged
3. Revoke read access: Revoked grant remains active

## Rules that hold
NoDeadlock
