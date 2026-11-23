# Admission

TLC reached 5 states and 5 steps. The chart draws 5 states and 4 arrows.
The chart does not draw 1 steps that stay in one chart state.

## Capacity breaks
The shortest path has 3 steps:
0. One handshake awaits admission
1. Authenticate and admit first session: First session active
2. Accept second transport: Second handshake awaits admission
3. Admit despite full capacity: Session capacity exceeded

## Rules that hold
NoDeadlock
