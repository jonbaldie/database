# PacketProtocol

TLC reached 9 states and 9 steps. The chart draws 3 states and 2 arrows.
The chart does not draw 3 steps that stay in one chart state.

## ValidMessageAccepted breaks
The shortest path has 2 steps:
0. Reading first frame
1. First frame arrives: Waiting for next frame
2. Next consecutive frame arrives: Valid message rejected

## Rules that hold
NoDeadlock
