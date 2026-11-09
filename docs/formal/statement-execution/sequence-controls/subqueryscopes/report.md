# SubqueryScopes

TLC reached 10 states and 10 steps. The chart draws 8 states and 8 arrows.
The chart does not draw 2 steps that stay in one chart state.

## ScopeControlsReuse breaks
The shortest path has 4 steps:
0. compile, dependent
1. Classify correlation and cache independent result: evaluate, dependent
2. Evaluate for outer row: next scope, dependent
3. Advance outer row: evaluate, dependent
4. Evaluate for outer row: Dependent query reused wrong scope

## Rules that hold
NoDeadlock
