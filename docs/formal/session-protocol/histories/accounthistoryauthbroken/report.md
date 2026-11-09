# AccountHistory

TLC reached 7 states and 7 steps. The chart draws 7 states and 6 arrows.
The chart does not draw 1 steps that stay in one chart state.

## DisabledAccountCannotAuthenticate breaks
The shortest path has 3 steps:
0. absent
1. Create account: created
2. Change password: changed
3. Lock account and revoke sessions: locked

## Rules that hold
DisableRevokesSession, NoDeadlock
