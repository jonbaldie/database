# UpdateSequence

TLC reached 6 states and 6 steps. The chart draws 6 states and 5 arrows.
The chart does not draw 1 steps that stay in one chart state.

## AssignmentsSeePriorValues breaks
The shortest path has 5 steps:
0. select
1. Select: first assignment
2. Assign First: second assignment
3. Assign Second: validate
4. Validate: publish
5. Publish: Wrong values published

## Rules that hold
NoDeadlock
