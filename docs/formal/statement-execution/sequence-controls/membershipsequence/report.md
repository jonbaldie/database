# MembershipSequence

TLC reached 12 states and 12 steps. The chart draws 6 states and 5 arrows.
The chart does not draw 3 steps that stay in one chart state.

## NullAwareMembership breaks
The shortest path has 3 steps:
0. Inspect member 1
1. Read: Inspect member 2
2. Read: Inspect member 3
3. Finish: Membership false; inputs null, other

## Rules that hold
NoDeadlock
