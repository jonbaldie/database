# AlterPublication

TLC reached 10 states and 10 steps. The chart draws 6 states and 5 arrows.
The chart does not draw 2 steps that stay in one chart state.

## ValidateBeforePublication breaks
The shortest path has 4 steps:
0. original schema
1. Clone: private schema
2. Apply private schema actions: actions applied
3. Validate: validated
4. Publish: Invalid schema published

## Rules that hold
NoDeadlock
