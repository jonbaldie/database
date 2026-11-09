# AdmissionDrain

TLC reached 7 states and 8 steps. The chart draws 7 states and 6 arrows.
The chart does not draw 2 steps that stay in one chart state.

## DrainWaitsForStatement breaks
The shortest path has 4 steps:
0. pending
1. Authentication completes; admit session: admitted
2. Client starts statement: executing
3. Operator requests drain; reject new work: draining
4. Server closes session while statement still runs: Session closed while statement runs

## Rules that hold
NoDeadlock
