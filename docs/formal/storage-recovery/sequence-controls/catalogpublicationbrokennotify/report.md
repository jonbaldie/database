# CatalogPublication

TLC reached 14 states and 18 steps. The chart draws 5 states and 6 arrows.
The chart does not draw 11 steps that stay in one chart state.

## OutcomeMatchesPublication breaks
The shortest path has 2 steps:
0. queued
1. Collect queued requests: batch collected
2. Apply isolated request changes: Success announced without durable publication

## Rules that hold
NoDeadlock
