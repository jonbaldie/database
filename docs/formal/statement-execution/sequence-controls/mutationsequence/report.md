# MutationSequence

TLC reached 9 states and 9 steps. The chart draws 6 states and 6 arrows.
The chart does not draw 1 steps that stay in one chart state.

## OrderedCandidates breaks
The shortest path has 8 steps:
0. plan
1. Plan: delete conflict
2. Delete conflicting row: insert candidate
3. Insert submitted row: next
4. Process next candidate: delete conflict
5. Delete conflicting row: insert candidate
6. Insert submitted row: next
7. Validate: validated
8. Publish: Wrong replacement published

## Rules that hold
NoDeadlock
