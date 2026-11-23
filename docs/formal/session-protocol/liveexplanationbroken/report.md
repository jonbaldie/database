# LiveExplanation

TLC reached 5 states and 5 steps. The chart draws 5 states and 4 arrows.
The chart does not draw 1 steps that stay in one chart state.

## CleanupOwnsEntry breaks
The shortest path has 3 steps:
0. No active explanation
1. Start the first statement: First statement registered
2. Start the next statement: Next statement registered
3. Finish cleanup for the first statement: Old cleanup removed the next statement

## Rules that hold
NoDeadlock
