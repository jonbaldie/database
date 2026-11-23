# ConnectExecute

TLC reached 11 states and 14 steps. The chart draws 11 states and 12 arrows.
The chart does not draw 2 steps that stay in one chart state.

## RunningHasWatch breaks
The shortest path has 6 steps:
0. Connect
1. Server sends greeting: Greeting sent
2. Password exchange succeeds: Credentials accepted
3. Session slot available: Ready
4. Client submits a supported text statement: Statement starting
5. Install cancellation watch: Cancellation available
6. Publish active statement: Statement active

## Rules that hold
CompletionHidesState, NoDeadlock
